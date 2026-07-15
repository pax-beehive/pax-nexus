FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/mem0-eval-configure ./cmd/mem0-eval-configure

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mem0-eval-configure /usr/local/bin/mem0-eval-configure
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/mem0-eval-configure"]
