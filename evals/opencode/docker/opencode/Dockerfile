FROM golang:1.25-bookworm AS team-memory-build

WORKDIR /src/team-memory
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/paxm-team-memory-provider ./cmd/paxm-team-memory-provider

FROM paxm AS paxm-source

FROM golang:1.25-bookworm AS paxm-build
WORKDIR /src/paxm
COPY --from=paxm-source . .
COPY evals/opencode/docker/exportplugin/main.go.txt ./cmd/export-opencode-plugin/main.go
RUN go build -trimpath -o /out/paxm ./cmd/paxm
RUN go run ./cmd/export-opencode-plugin > /out/paxm.js

FROM node:22-bookworm-slim

ARG OPENCODE_VERSION=1.17.20
RUN npm install --global "opencode-ai@${OPENCODE_VERSION}"
COPY --from=paxm-build /out/paxm /usr/local/bin/paxm
COPY --from=paxm-build /out/paxm.js /opt/paxm/paxm.js
COPY --from=team-memory-build /out/paxm-team-memory-provider /usr/local/bin/paxm-team-memory-provider
COPY evals/opencode/docker/opencode/entrypoint.sh /usr/local/bin/team-memory-opencode
RUN chmod 0755 /usr/local/bin/team-memory-opencode

WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/team-memory-opencode"]
