FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY test/e2e/demo-client/ ./test/e2e/demo-client/
RUN CGO_ENABLED=0 go build -trimpath -o /out/demo-client ./test/e2e/demo-client
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/demo-client /demo-client
ENTRYPOINT ["/demo-client"]
