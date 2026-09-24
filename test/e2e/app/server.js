// Sample app for the end-to-end test: answers its version and one env var,
// logs every request, and has a /health endpoint.
const http = require("http");
const version = process.env.APP_VERSION || "v1";
http.createServer((req, res) => {
  console.log(`request ${req.url}`);
  if (req.url === "/health") return res.end("ok");
  res.end(`${version} ${process.env.GREETING || ""}`.trim() + "\n");
}).listen(3000);
