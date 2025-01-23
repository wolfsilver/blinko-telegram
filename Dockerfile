# Build stage
FROM cgr.dev/chainguard/go:latest AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o blinkogram ./bin/blinkogram
RUN chmod +x blinkogram

# Run stage
FROM cgr.dev/chainguard/static:latest-glibc
WORKDIR /app
ENV SERVER_ADDR=dns:localhost:5230
ENV BOT_TOKEN=your_telegram_bot_token
COPY .env.example .env
COPY --from=builder /app/blinkogram .
CMD ["./blinkogram"]
