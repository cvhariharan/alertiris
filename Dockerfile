FROM golang:1.25.3-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /alertiris .

FROM debian:bookworm-slim

COPY --from=builder /alertiris /alertiris

ENTRYPOINT ["/alertiris"]
