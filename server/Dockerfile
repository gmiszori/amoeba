FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/amoeba-server .

# `docker build --target test server` runs the test suite.
FROM build AS test
RUN go vet ./... && go test ./...

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/amoeba-server /amoeba-server
EXPOSE 8080
ENTRYPOINT ["/amoeba-server"]
