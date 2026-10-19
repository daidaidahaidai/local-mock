FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY main.go .
RUN CGO_ENABLED=0 go build -trimpath -o /app main.go
FROM scratch
COPY --from=build /app /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app"]
