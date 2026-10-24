FROM golang:1.25-bookworm AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 go build -trimpath -o /app .
FROM scratch
COPY --from=build /app /app
USER 65532:65532
ENTRYPOINT ["/app"]
