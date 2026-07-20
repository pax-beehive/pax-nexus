ARG GO_IMAGE=golang:1.25-alpine
ARG DOCKER_CLI_IMAGE=docker:27.5.1-cli

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/team-memory-eval-v2 ./cmd/team-memory-eval-v2
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/groupmembench-select ./cmd/groupmembench-select
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eval-v2-zep ./cmd/eval-v2-zep

FROM ${DOCKER_CLI_IMAGE}
RUN apk add --no-cache curl git jq util-linux
COPY --from=build /out/team-memory-eval-v2 /usr/local/bin/team-memory-eval-v2
COPY --from=build /out/groupmembench-select /usr/local/bin/groupmembench-select
COPY --from=build /out/eval-v2-zep /usr/local/bin/eval-v2-zep
WORKDIR /workspace
ENTRYPOINT ["/bin/sh"]
