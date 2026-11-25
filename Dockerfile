# ---- build ----
FROM golang:1.26-alpine AS build
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN go build -trimpath -ldflags="-w -s -X github.com/fengcunhan/new-api-mcp/internal/tools/ops.Version=${VERSION}" \
      -o /out/new-api-mcp ./cmd/new-api-mcp

# ---- run ----
FROM alpine:3.21
RUN apk add --no-cache tzdata ca-certificates && adduser -D -u 10001 mcp
ENV MCP_TRANSPORT=http MCP_LISTEN=0.0.0.0:8080
COPY --from=build /out/new-api-mcp /usr/local/bin/new-api-mcp
USER mcp
EXPOSE 8080
CMD ["/usr/local/bin/new-api-mcp"]
