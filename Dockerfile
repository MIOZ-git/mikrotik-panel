FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY index.html ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" -o /panel .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /panel /panel
EXPOSE 8000
ENTRYPOINT ["/panel"]