FROM golang:1.26.0 AS builder
WORKDIR /app
COPY go.mod .
COPY main.go .
RUN CGO_ENABLED=0 go build -o /mock-github .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /mock-github /mock-github
ENTRYPOINT ["/mock-github"]
