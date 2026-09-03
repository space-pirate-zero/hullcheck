# The documented entry point:
#   docker run --rm -v "$PWD:/repo:ro" ghcr.io/spaceship-alpha-9/hullcheck
# The mount is read-only, so "never writes to your repository" is enforced by the
# kernel rather than by our good intentions, and --rm removes the container on exit.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /hullcheck ./cmd/hullcheck

# scratch, not alpine: nothing to exec, nothing to exfiltrate with.
FROM scratch
COPY --from=build /hullcheck /hullcheck
WORKDIR /repo
ENTRYPOINT ["/hullcheck"]
CMD ["."]
