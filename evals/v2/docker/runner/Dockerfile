ARG GO_IMAGE=golang:1.25-alpine
ARG DOCKER_CLI_IMAGE=docker:27.5.1-cli

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/team-memory-eval-v2 ./cmd/team-memory-eval-v2
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/groupmembench-select ./cmd/groupmembench-select

FROM ${DOCKER_CLI_IMAGE}
RUN apk add --no-cache curl git jq util-linux
COPY --from=build /out/team-memory-eval-v2 /usr/local/bin/team-memory-eval-v2
COPY --from=build /out/groupmembench-select /usr/local/bin/groupmembench-select
WORKDIR /workspace
ENTRYPOINT ["/bin/sh"]
