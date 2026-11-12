FROM node:22-bookworm-slim AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci --no-audit --no-fund
COPY . /src/
RUN npm run build && for ext in png jpg jpeg webp; do if [ -f /src/image/avatar.$ext ]; then cp /src/image/avatar.$ext ./dist/avatar.$ext; fi; done

FROM golang:1.26-bookworm AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app/tg-attendance .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/* && useradd --system --uid 10001 app
WORKDIR /app
COPY --from=backend /app/tg-attendance ./tg-attendance
COPY --from=frontend /src/frontend/dist ./public
ENV ENV=production FRONTEND_DIST=/app/public TZ=Asia/Shanghai GOMEMLIMIT=256MiB GOGC=70
USER app
EXPOSE 8080
CMD ["/app/tg-attendance"]
