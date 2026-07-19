FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG EXTRACTION_CANDIDATE_STRATEGY=current
RUN case "${EXTRACTION_CANDIDATE_STRATEGY}" in \
      current|interaction-slim|typed-2) ;; \
      *) echo "unsupported EXTRACTION_CANDIDATE_STRATEGY=${EXTRACTION_CANDIDATE_STRATEGY}" >&2; exit 2 ;; \
    esac && \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X github.com/pax-beehive/pax-nexus/internal/teamnote/extractor.buildDefaultCandidateStrategy=${EXTRACTION_CANDIDATE_STRATEGY}" \
      -o /out/team-memory .
RUN CGO_ENABLED=0 go build -trimpath -o /out/paxm-team-memory-provider ./cmd/paxm-team-memory-provider

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/team-memory /usr/local/bin/team-memory
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/team-memory"]
