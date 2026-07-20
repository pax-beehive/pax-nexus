FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG EXTRACTION_CANDIDATE_STRATEGY=current
ARG RECALL_CANDIDATE_STRATEGY=passive-v1
RUN case "${EXTRACTION_CANDIDATE_STRATEGY}" in \
      current|interaction-slim|evidence-fidelity-v1|typed-2|source-span-v1|source-span-v2|claim-card-v1|claim-card-v2) ;; \
      *) echo "unsupported EXTRACTION_CANDIDATE_STRATEGY=${EXTRACTION_CANDIDATE_STRATEGY}" >&2; exit 2 ;; \
    esac && \
    case "${RECALL_CANDIDATE_STRATEGY}" in \
      passive-v1|hint-v1-selective) ;; \
      *) echo "unsupported RECALL_CANDIDATE_STRATEGY=${RECALL_CANDIDATE_STRATEGY}" >&2; exit 2 ;; \
    esac && \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X github.com/pax-beehive/pax-nexus/internal/teamnote/extractor.buildDefaultCandidateStrategy=${EXTRACTION_CANDIDATE_STRATEGY} -X github.com/pax-beehive/pax-nexus/internal/teamnote.buildDefaultRecallCandidateStrategy=${RECALL_CANDIDATE_STRATEGY}" \
      -o /out/team-memory .
RUN CGO_ENABLED=0 go build -trimpath -o /out/paxm-team-memory-provider ./cmd/paxm-team-memory-provider

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/team-memory /usr/local/bin/team-memory
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/team-memory"]
