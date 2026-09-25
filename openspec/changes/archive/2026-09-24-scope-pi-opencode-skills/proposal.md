## Why

Pi currently receives Argus routing text but no built-in skill bodies. OpenCode receives the bodies through a global config entry, exposing Argus workflows outside Argus.

## What Changes

- Pass Argus's managed skill directory to Pi with a per-launch `--skill` flag.
- Pass it to OpenCode through child-only `OPENCODE_CONFIG_CONTENT`, preserving existing inline settings.
- Stop globally injecting the OpenCode skill path and remove the path previously inserted by Argus.

## Impact

Pi and OpenCode gain Argus skills only in Argus-launched sessions. Existing user skills and OpenCode MCP registration remain available.
