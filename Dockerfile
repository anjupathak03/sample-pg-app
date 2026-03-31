# ---------- build ----------
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app main.go

# ---------- runtime ----------
FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /app /app
EXPOSE 8080
ENTRYPOINT ["/app"]
