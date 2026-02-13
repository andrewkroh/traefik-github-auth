FROM golang:1.26.0 AS builder
WORKDIR /app
COPY go.mod .
COPY main.go .
RUN CGO_ENABLED=0 go build -o /echo .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /echo /echo
ENTRYPOINT ["/echo"]
