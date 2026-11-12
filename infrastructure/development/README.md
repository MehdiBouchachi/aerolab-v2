# Development infrastructure

The **tested** way to run a dev site is `make dev-stack` (see the root README): it runs the
host's `mosquitto`, the `edge` binary and N simulated controllers, all with generated
random credentials in the git-ignored `.dev/` directory.

There is deliberately **no `compose.yaml` yet**: the generated broker config contains host
paths, so a container variant needs a path-templating step that has not been built or
tested. Dockerised edge/simulator, PostgreSQL and the cloud API arrive with Phase 4
(`infrastructure/cloud`).
