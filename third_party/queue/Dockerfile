# Central pull-based job queue service.
FROM golang:1.25 AS builder
WORKDIR /src
COPY queue/go.mod ./
RUN go mod download
COPY queue/ ./
RUN CGO_ENABLED=0 go build -o /out/queued ./cmd/queued

FROM scratch
COPY --from=builder /out/queued /usr/local/bin/queued
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/queued"]
