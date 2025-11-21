#!/usr/bin/env node
import { Command } from "commander";
import { initCommand } from "./commands/init";
import { provisionCommand } from "./commands/provision";
import { deployCommand } from "./commands/deploy";
import { listCommand } from "./commands/list";
import { removeCommand } from "./commands/remove";

const program = new Command();

export const asciiArt = `                             
                                 ..:^~!!7!!~^.              
                           .^!J5GB##BBGGGGGB##B5!           
                       :!YG##G5J7~:..       .^?G@B^         
                    ^JG#B57^.                   ?@B.        
                 :?B&BJ^                       !Y@&^        
               :Y&&Y^       .:^~~~~~^:        5@@@@G        
              7&@J.      ^JG#@@@@@@@@&B57.    5@@@&?        
             J@#^      !G@@@GY?!!!!7JP#@@&? :Y&#7^.         
            !@&:      7@@@5:          .~5P75@#?.            
            5@P       B@@B              ~P&#?.              
            !@#:      5@@&J:         .7B@G!.                
             J@B^     .Y&@@&PJ~:   .J##Y^                   
              !#@5^     :75B@@@@BP?!!~                      
               .7B@B?^     .:!JPB&@@@#P?~.                  
                  ~Y#&#57^.      :~75B&@@#Y^                
                     :?P#&#GY7^.      :!YB@@P^              
                    ^YB5^^7YG&@&GJ~.      !B@&~             
                 .7B@G7:     .~?B@@#7      .G@#.            
               ^Y&&5^            ?@@@~      ~@@~            
             ~P@B7^              ?@@@!      ?@&:            
           :P@B!:5&BJ~.      .:!5@@@Y      !@@7             
          7&&7  .7G&@@&BGGGGB#@@@#5~     .Y@#~              
         7@B:      :!J5GBBBBGPY?~.     :?&&J.               
        ^@&:                        .!5&#J:                 
        !@G                      :75##5!.                   
        .P@5^              .:~?5B#GY~.                      
          7G#BPY?77!77?JYPGBBBPJ!:                          
            :~?J55PPPP5YJ7!^.                               
`;

program
  .name("sagansync")
  .description(
    "Instantly deploy your local project to a VPS — real-time sync, HTTPS, and zero CI/CD."
  )
  .version("0.0.1");

program
  .command("init")
  .description("Initialize project configuration for SaganSync")
  .action(initCommand);

program
  .command("provision")
  .description("Provision the VPS with Podman and Caddy via SSH")
  .option(
    "-c, --clean",
    "Remove previous installations of Caddy and Podman before starting"
  )
  .option(
    "-f, --force",
    "Skip confirmation prompts (overwrite existing provision)"
  )
  .action(provisionCommand);

program
  .command("deploy")
  .description("Deploy the project to the VPS")
  .option("-w, --workspace <name>", "Override workspace name")
  .option(
    "-f, --force",
    "Skip confirmation prompts (overwrite existing deploy)"
  )
  .option("-v, --verbose", "Show detailed logs (podman stop/rm/etc)")
  .action(deployCommand);

program
  .command("list")
  .alias("ls")
  .description("List all active deployments from SaganSync")
  .action(listCommand);

program
  .command("remove")
  .alias("rm")
  .description("Remove a deployment from SaganSync")
  .option("--full", "Also remove the associated container image and volumes")
  .option("-v, --verbose", "Show detailed logs")
  .action(removeCommand);

if (!process.argv.slice(2).length) {
  console.log(asciiArt);
  program.outputHelp();
} else {
  program.parse();
}
