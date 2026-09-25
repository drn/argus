## MODIFIED Requirements

### Requirement: Session-scoped Pi and OpenCode builtin skills

Argus SHALL deliver embedded skills to Pi through a per-launch `--skill` flag and to OpenCode through child-only `OPENCODE_CONFIG_CONTENT`. It SHALL preserve existing user skills and inline configuration and SHALL remove only its previously injected skill path from OpenCode's global config.
