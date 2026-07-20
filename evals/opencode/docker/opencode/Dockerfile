FROM golang:1.25-bookworm AS team-memory-build

WORKDIR /src/team-memory
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/paxm-team-memory-provider ./cmd/paxm-team-memory-provider
RUN CGO_ENABLED=0 go build -trimpath -o /out/eval-v2-memory ./cmd/eval-v2-memory
RUN CGO_ENABLED=0 go build -trimpath -o /out/eval-v2-bm25 ./cmd/eval-v2-bm25
RUN CGO_ENABLED=0 go build -trimpath -o /out/eval-v2-active-recall ./cmd/eval-v2-active-recall
RUN CGO_ENABLED=0 go build -trimpath -o /out/recall-eval-v2-seed ./cmd/recall-eval-v2-seed

FROM paxm AS paxm-source

FROM golang:1.25-bookworm AS paxm-build
ARG PAXM_VERSION=v0.2.0
WORKDIR /src/paxm
COPY --from=paxm-source . .
COPY evals/opencode/docker/exportplugin/main.go.txt ./cmd/export-opencode-plugin/main.go
RUN test "$(git describe --tags --exact-match HEAD)" = "${PAXM_VERSION}"
RUN go build -trimpath -ldflags "-s -w -X main.version=${PAXM_VERSION}" -o /out/paxm ./cmd/paxm
RUN go run ./cmd/export-opencode-plugin > /out/paxm.js

FROM node:22-bookworm-slim

ARG OPENCODE_VERSION=1.17.20
RUN npm install --global "opencode-ai@${OPENCODE_VERSION}"
COPY --from=paxm-build /out/paxm /usr/local/bin/paxm
COPY --from=paxm-build /out/paxm.js /opt/paxm/paxm.js
COPY --from=team-memory-build /out/paxm-team-memory-provider /usr/local/bin/paxm-team-memory-provider
COPY --from=team-memory-build /out/eval-v2-memory /usr/local/bin/eval-v2-memory
COPY --from=team-memory-build /out/eval-v2-bm25 /usr/local/bin/eval-v2-bm25
COPY --from=team-memory-build /out/eval-v2-active-recall /usr/local/bin/eval-v2-active-recall
COPY --from=team-memory-build /out/recall-eval-v2-seed /usr/local/bin/recall-eval-v2-seed
COPY evals/opencode/docker/opencode/active_recall.ts /opt/team-memory/active_recall.ts
COPY evals/opencode/docker/opencode/ingest-private-sqlite.mjs /opt/team-memory/ingest-private-sqlite.mjs
COPY evals/opencode/docker/opencode/entrypoint.sh /usr/local/bin/team-memory-opencode
RUN chmod 0755 /usr/local/bin/team-memory-opencode

WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/team-memory-opencode"]
