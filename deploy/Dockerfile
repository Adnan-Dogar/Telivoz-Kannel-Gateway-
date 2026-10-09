# Builds the portal and the gateway into one small image.
# docker build -f deploy/Dockerfile -t telivoz-gateway .

FROM node:22-alpine AS web
WORKDIR /src/apps/web
RUN corepack enable
COPY apps/web/package.json apps/web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY apps/web/ ./
RUN pnpm build

FROM golang:1.24-alpine AS go
WORKDIR /src/gateway
COPY gateway/go.mod gateway/go.sum ./
RUN go mod download
COPY gateway/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/gateway ./cmd/gateway \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/smppsim ./cmd/smppsim

# The Alpine base already carries the CA bundle, and the binary embeds time zone data, so nothing is downloaded here.
FROM alpine:3.21
RUN adduser -D -H -u 10001 gateway
COPY --from=go /out/ /usr/local/bin/
COPY --from=web /src/apps/web/dist /srv/web
USER gateway
ENV WEB_DIR=/srv/web HTTP_ADDR=:8080 SMPP_ADDR=:2775
EXPOSE 8080 2775
ENTRYPOINT ["gateway"]
CMD ["serve"]
