FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY tests/onprem-e2e ./tests/onprem-e2e
RUN CGO_ENABLED=0 go build -trimpath -o /out/mock-extractor ./tests/onprem-e2e/mockextractor
RUN CGO_ENABLED=0 go build -trimpath -o /out/mock-oidc ./tests/onprem-e2e/mockoidc
RUN CGO_ENABLED=0 go test -c -o /out/core-flow ./tests/onprem-e2e

FROM gcr.io/distroless/static-debian12:nonroot AS mock-extractor
COPY --from=build /out/mock-extractor /usr/local/bin/mock-extractor
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/mock-extractor"]

FROM gcr.io/distroless/static-debian12:nonroot AS mock-oidc
COPY --from=build /out/mock-oidc /usr/local/bin/mock-oidc
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/mock-oidc"]

FROM gcr.io/distroless/static-debian12:nonroot AS e2e
COPY --from=build /out/core-flow /usr/local/bin/core-flow
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/core-flow", "-test.v", "-test.count=1"]
