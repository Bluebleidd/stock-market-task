FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o stock-market ./cmd/server/

FROM alpine:3.21
WORKDIR /root/

COPY --from=builder /app/stock-market .
ENTRYPOINT ["./stock-market"]