FROM golang:1.26-alpine AS builder
WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN go build -o stock-market .

FROM alpine:latest
WORKDIR /root/

COPY --from=builder /app/stock-market .
ENTRYPOINT ["./stock-market"]