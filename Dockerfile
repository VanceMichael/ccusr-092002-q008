
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.22
WORKDIR /app
COPY --from=build /out/server /app/server
ENV PORT=8080 DATABASE_PATH=/data/app.sqlite3
EXPOSE 8080
# 迁移脚本已内嵌进二进制，服务启动时自动应用，无需在镜像内安装 sqlite 客户端。
CMD ["/bin/sh","-c","mkdir -p /data && exec /app/server"]
