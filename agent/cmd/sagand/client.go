package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/borgim/sagansync/agent/internal/api"
	"github.com/borgim/sagansync/agent/internal/deploy"
	"github.com/borgim/sagansync/agent/internal/domains"
	"github.com/borgim/sagansync/agent/internal/events"
)

const defaultSocket = "/run/sagand/sagand.sock"

type requestFlags struct {
	project, workspace, domain, previewDomain, healthPath, sha string
	port, healthTimeout                                        int
}

func (f *requestFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.project, "project", "", "project name")
	fs.StringVar(&f.workspace, "workspace", "", "workspace name")
	fs.StringVar(&f.domain, "domain", "", "production domain")
	fs.StringVar(&f.previewDomain, "preview-domain", "", "base domain for branch workspaces")
	fs.StringVar(&f.healthPath, "health-path", "", "HTTP health check path")
	fs.StringVar(&f.sha, "sha", "", "git commit sha")
	fs.IntVar(&f.port, "port", 0, "port the app listens on inside the container")
	fs.IntVar(&f.healthTimeout, "health-timeout", 60, "health check timeout in seconds")
}

func (f *requestFlags) query() url.Values {
	return url.Values{"project": {f.project}, "workspace": {f.workspace}, "domain": {f.domain},
		"previewDomain": {f.previewDomain}, "healthPath": {f.healthPath}, "sha": {f.sha},
		"port": {strconv.Itoa(f.port)}, "healthTimeout": {strconv.Itoa(f.healthTimeout)}}
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func workspaceFlags(fs *flag.FlagSet) (*string, *string) {
	return fs.String("project", "", "project name"), fs.String("workspace", "", "workspace name")
}

// fail writes an error event to stdout and returns its exit code.
func fail(stdout io.Writer, code, msg string) int {
	e := events.Fail(code, msg, nil)
	events.NewWriter(stdout).Emit(e)
	return events.ExitCode(e)
}

func runClient(args []string, stdin io.Reader, stdout io.Writer) int {
	sock := os.Getenv("SAGAND_SOCKET")
	if sock == "" {
		sock = defaultSocket
	}
	c := api.NewClient(sock)
	ctx := context.Background()
	cmd, rest := args[0], args[1:]
	fs := newFlagSet(cmd)

	switch cmd {
	case "version":
		_ = json.NewEncoder(stdout).Encode(api.VersionInfo{Version: version, Protocol: events.Protocol})
		return 0

	case "host":
		var f requestFlags
		f.register(fs)
		if err := fs.Parse(rest); err != nil {
			return fail(stdout, deploy.CodeInvalid, err.Error())
		}
		_ = json.NewEncoder(stdout).Encode(map[string]string{"host": domains.Host(f.project, f.workspace, f.domain, f.previewDomain)})
		return 0

	case "deploy":
		var f requestFlags
		f.register(fs)
		if err := fs.Parse(rest); err != nil {
			return fail(stdout, deploy.CodeInvalid, err.Error())
		}
		return stream(ctx, c, http.MethodPost, "/v1/deploy", f.query(), stdin, stdout)

	case "dev":
		var f requestFlags
		f.register(fs)
		command := fs.String("command", "", "dev command as a JSON array")
		build := fs.Bool("build", false, "rebuild the dev image")
		force := fs.Bool("force", false, "allow dev mode on production")
		if err := fs.Parse(rest); err != nil {
			return fail(stdout, deploy.CodeInvalid, err.Error())
		}
		q := f.query()
		q.Set("command", *command)
		q.Set("build", strconv.FormatBool(*build))
		q.Set("force", strconv.FormatBool(*force))
		return stream(ctx, c, http.MethodPost, "/v1/dev", q, stdin, stdout)

	case "remove":
		p, w := workspaceFlags(fs)
		if err := fs.Parse(rest); err != nil {
			return fail(stdout, deploy.CodeInvalid, err.Error())
		}
		return stream(ctx, c, http.MethodPost, "/v1/remove", url.Values{"project": {*p}, "workspace": {*w}}, nil, stdout)

	case "list":
		return simple(ctx, c, http.MethodGet, "/v1/list", nil, nil, stdout)

	case "logs":
		p, w := workspaceFlags(fs)
		tail := fs.Int("tail", 100, "lines from the end")
		follow := fs.Bool("f", false, "follow")
		if err := fs.Parse(rest); err != nil {
			return fail(stdout, deploy.CodeInvalid, err.Error())
		}
		q := url.Values{"project": {*p}, "workspace": {*w}, "tail": {strconv.Itoa(*tail)}, "follow": {strconv.FormatBool(*follow)}}
		return simple(ctx, c, http.MethodGet, "/v1/logs", q, nil, stdout)

	case "put", "rm":
		if len(rest) != 3 {
			return fail(stdout, deploy.CodeInvalid, "usage: sagand "+cmd+" <project> <workspace> <path>")
		}
		q := url.Values{"project": {rest[0]}, "workspace": {rest[1]}, "path": {rest[2]}}
		if cmd == "put" {
			return simple(ctx, c, http.MethodPut, "/v1/files", q, stdin, stdout)
		}
		return simple(ctx, c, http.MethodDelete, "/v1/files", q, nil, stdout)

	case "env":
		return envCmd(ctx, c, rest, stdin, stdout)
	}
	return fail(stdout, deploy.CodeInvalid, "unknown command "+cmd)
}

func envCmd(ctx context.Context, c *api.Client, args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		return fail(stdout, deploy.CodeInvalid, "usage: sagand env list|set|unset --project p --workspace w [KEY...]")
	}
	sub := args[0]
	fs := newFlagSet("env")
	p, w := workspaceFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return fail(stdout, deploy.CodeInvalid, err.Error())
	}
	q := url.Values{"project": {*p}, "workspace": {*w}}
	switch sub {
	case "list":
		return simple(ctx, c, http.MethodGet, "/v1/env", q, nil, stdout)
	case "set":
		return simple(ctx, c, http.MethodPost, "/v1/env", q, stdin, stdout)
	case "unset":
		q["key"] = fs.Args()
		return simple(ctx, c, http.MethodDelete, "/v1/env", q, nil, stdout)
	}
	return fail(stdout, deploy.CodeInvalid, "unknown env subcommand "+sub)
}

func unavailable(stdout io.Writer, err error) int {
	return fail(stdout, "daemon_unavailable",
		fmt.Sprintf("cannot reach the sagand daemon (%v); an admin can check it with: systemctl status sagand", err))
}

func errorResponse(resp *http.Response, stdout io.Writer) int {
	var e struct{ Code, Message string }
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
	if e.Code == "" {
		e.Code, e.Message = deploy.CodeInternal, "daemon returned HTTP "+strconv.Itoa(resp.StatusCode)
	}
	return fail(stdout, e.Code, e.Message)
}

// stream relays NDJSON events and exits according to the last terminal event.
func stream(ctx context.Context, c *api.Client, method, path string, q url.Values, body io.Reader, stdout io.Writer) int {
	resp, err := c.Do(ctx, method, path, q, body)
	if err != nil {
		return unavailable(stdout, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errorResponse(resp, stdout)
	}
	code := 1 // a stream that ends without done/error is a failure
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		stdout.Write(line)
		stdout.Write([]byte("\n"))
		var e events.Event
		if json.Unmarshal(line, &e) == nil && (e.Type == "done" || e.Type == "error") {
			code = events.ExitCode(e)
		}
	}
	return code
}

// simple copies a successful response body to stdout.
func simple(ctx context.Context, c *api.Client, method, path string, q url.Values, body io.Reader, stdout io.Writer) int {
	resp, err := c.Do(ctx, method, path, q, body)
	if err != nil {
		return unavailable(stdout, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return errorResponse(resp, stdout)
	}
	if _, err := io.Copy(stdout, resp.Body); err != nil {
		return 1
	}
	return 0
}
