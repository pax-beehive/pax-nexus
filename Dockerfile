FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/team-memory .
RUN CGO_ENABLED=0 go build -trimpath -o /out/paxm-team-memory-provider ./cmd/paxm-team-memory-provider

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/team-memory /usr/local/bin/team-memory
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/team-memory"]
