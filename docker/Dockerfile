# syntax=docker/dockerfile:1.7

# ---- build stage ----
FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/kiri ./cmd/kiri

# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kiri /kiri

EXPOSE 4443 8085
USER nonroot:nonroot

ENTRYPOINT ["/kiri"]
