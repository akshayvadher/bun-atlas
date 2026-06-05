# syntax=docker/dockerfile:1

# ---- build stage ----------------------------------------------------------
# Compiles the API server into a single static binary. CGO is disabled because
# the Postgres driver (bun + pgdriver) is pure Go, so the result needs no libc.
FROM golang:1.26 AS build

WORKDIR /src

# Cache deps separately from source so `go mod download` only re-runs when
# go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---- runtime stage --------------------------------------------------------
# distroless/static holds only CA certs + tzdata + a nonroot user — no shell,
# no package manager. Smallest reasonable attack surface for a static Go binary.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/server /server

# Server reads PORT (default 8080) and DATABASE_URL from the environment.
EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/server"]
