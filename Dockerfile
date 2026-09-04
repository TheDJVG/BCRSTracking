# Build Stage
FROM golang:1.27-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /bin/bcrs-tracker ./cmd/tracker

# Final Stage (Distroless / Non-root)
FROM gcr.io/distroless/static-debian13:nonroot

USER nonroot:nonroot

COPY --from=builder /bin/bcrs-tracker /bin/bcrs-tracker

EXPOSE 9090

ENTRYPOINT ["/bin/bcrs-tracker"]
