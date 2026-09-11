# syntax=docker/dockerfile:1.7

# ─── build stage ────────────────────────────────────────────────────────
FROM node:22-alpine AS build
WORKDIR /app

# Bake API base into the bundle at build time. EasyPanel passes this as
# a build arg; locally `make build` leaves it empty so the bundle uses
# relative (same-origin) paths.
#
#   docker build --build-arg VITE_API_URL=https://api.corsi.dev ./frontend
ARG VITE_API_URL=""
ENV VITE_API_URL=${VITE_API_URL}

COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build

# ─── runtime stage ──────────────────────────────────────────────────────
FROM nginx:1.27-alpine

# Drop the stock nginx default and replace with our SPA config.
RUN rm /etc/nginx/conf.d/default.conf
COPY nginx.conf /etc/nginx/conf.d/corsi.conf

COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 3000

# Pid file in /tmp keeps nginx happy when run by a non-root user later.
CMD ["nginx", "-g", "daemon off;"]
