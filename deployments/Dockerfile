FROM golang:1.25-alpine AS build

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/sentinel ./cmd/sentinel

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/sentinel /app/sentinel
COPY configs /app/configs
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/sentinel"]
