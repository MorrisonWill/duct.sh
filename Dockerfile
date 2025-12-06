FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /duct ./cmd/duct

FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /duct /duct

RUN mkdir -p /data

EXPOSE 22 80

ENV HOST_KEY_PATH=/data/host_key
ENV BASE_DOMAIN=duct.sh
ENV SSH_PORT=22
ENV HTTP_PORT=80

ENTRYPOINT ["/duct"]
