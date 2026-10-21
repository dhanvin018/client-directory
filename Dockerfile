FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/client-directory ./internal/main

FROM alpine:3.22
RUN apk add --no-cache postgresql-client jq \
	&& adduser -D -H app
WORKDIR /app
COPY config.json ./config.json
COPY db ./db
COPY scripts ./scripts
COPY --from=build /out/client-directory /usr/local/bin/client-directory
RUN chmod +x scripts/*.sh
USER app
# Read by both scripts/migrate.sh and the server. Mount a different file here
# (or override single keys with env vars) per environment.
ENV CONFIG_FILE=/app/config.json
# Keep in sync with server.port in config.json.
EXPOSE 8080
ENTRYPOINT ["/app/scripts/docker-entrypoint.sh"]
CMD ["client-directory"]
