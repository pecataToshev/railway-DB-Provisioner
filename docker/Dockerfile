# Provisioner base image — deployed to Railway as a one-shot service.
# Build context: repo root
#   docker build -f docker/Dockerfile -t railway-db-provisioner .
#
# This image contains only the binary. Consumers provide their own services.txt
# by extending the base image:
#   FROM ghcr.io/pecatatoshev/railway-db-provisioner:latest
#   COPY services.txt /app/services.txt

FROM golang:1.26-alpine3.24 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /provisioner ./cmd/provisioner

FROM alpine:3.24
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /provisioner /app/provisioner
CMD ["/app/provisioner"]
