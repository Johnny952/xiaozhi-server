# Upstream

Vendored from https://github.com/AnimeAIChat/xiaozhi-server-go at commit `0b1575cb` (without `.git`).

Baseline tests (golang:1.24-alpine): `src/httpsvr/ota`, `src/configs`, `src/configs/database` pass. `src/core/utils` `TestGetProjectDir` fails because it expects the working directory path to contain `xiaozhi-server-go` (the container mounts the source at `/src`); environmental, not a code defect.
