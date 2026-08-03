# The portal is served as static files from object storage behind the load
# balancer, same-origin with /v1 (see docs/saas-plan.md). It is still built
# here so the assets and the API binary come out of one build and share the
# image digest the pipeline promotes: a separately built frontend can drift
# from the API it talks to.
#
# CI exports the assets without running the API image:
#   docker buildx build --target web-assets --output type=local,dest=out/web .
FROM node:22-bookworm AS web-build
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# npm run build is "tsc --noEmit && vite build", so a type error fails the
# image build rather than shipping a broken portal.
RUN npm run build

# web-assets exists only to be exported; it must not be the final stage.
FROM scratch AS web-assets
COPY --from=web-build /web/dist /

FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG EXTRACTION_CANDIDATE_STRATEGY=source-clause-v1
ARG RECALL_CANDIDATE_STRATEGY=passive-v1
RUN case "${EXTRACTION_CANDIDATE_STRATEGY}" in \
      interaction-slim|source-clause-v1|source-span-v1|source-span-v2|claim-card-v2) ;; \
      *) echo "unsupported EXTRACTION_CANDIDATE_STRATEGY=${EXTRACTION_CANDIDATE_STRATEGY}" >&2; exit 2 ;; \
    esac && \
    case "${RECALL_CANDIDATE_STRATEGY}" in \
      passive-v1|hint-v1-selective) ;; \
      *) echo "unsupported RECALL_CANDIDATE_STRATEGY=${RECALL_CANDIDATE_STRATEGY}" >&2; exit 2 ;; \
    esac && \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-X github.com/pax-beehive/pax-nexus/internal/teamnote/extractor.buildDefaultCandidateStrategy=${EXTRACTION_CANDIDATE_STRATEGY} -X github.com/pax-beehive/pax-nexus/internal/teamnote.buildDefaultRecallCandidateStrategy=${RECALL_CANDIDATE_STRATEGY}" \
      -o /out/team-memory ./cmd/team-memory-onprem
# The migration job runs as a one-shot before instances roll out; it ships in
# the same image so the schema it applies always matches this binary.
RUN CGO_ENABLED=0 go build -trimpath -o /out/team-memory-migrate ./cmd/team-memory-migrate
RUN CGO_ENABLED=0 go build -trimpath -o /out/paxm-team-memory-provider ./cmd/paxm-team-memory-provider

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/team-memory /usr/local/bin/team-memory
COPY --from=build /out/team-memory-migrate /usr/local/bin/team-memory-migrate
COPY --from=build /out/paxm-team-memory-provider /usr/local/bin/paxm-team-memory-provider
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/team-memory"]
