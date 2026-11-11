# Security

Please report a vulnerability privately through
[GitHub's security advisories](https://github.com/MartyJRE/claudia/security/advisories/new),
not in a public issue. Only the latest release gets fixes.

claudia runs locally with your own credentials: Atlassian and GitLab tokens
from the environment and glab's login, and a Teams token in its state dir
(`~/.local/state/claudia`). It sends them only to the services they belong to.
The autopilot starts Claude sessions with `autopilot.permission_mode`; anything
looser than the default `auto` (such as `bypassPermissions`) runs without a
sandbox.
