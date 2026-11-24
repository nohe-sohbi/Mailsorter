# Build stage
# Same Node major as CI (.github/workflows/ci.yml).
FROM node:22-alpine AS build

WORKDIR /app

# Copy package files
COPY package*.json ./

# Install exactly what package-lock.json pins
RUN npm ci --silent

# Copy source code
COPY . .

# Set API URL at build time.
# Default to "/" so the bundle calls the API same-origin through the nginx proxy
# below (location /api -> backend:8080). Avoids baking a host-specific URL into
# the static build and sidesteps CORS in production. Override only if the API is
# served from a different origin.
ARG REACT_APP_API_URL=/
ENV REACT_APP_API_URL=$REACT_APP_API_URL

# Umami website id, also inlined at build time. Left empty, the tracker is
# simply never injected, which is the right default for local builds.
ARG REACT_APP_UMAMI_WEBSITE_ID=
ENV REACT_APP_UMAMI_WEBSITE_ID=$REACT_APP_UMAMI_WEBSITE_ID

# Build the app
RUN npm run build

# Production stage
FROM nginx:alpine

# Copy built app to nginx
COPY --from=build /app/build /usr/share/nginx/html

# Copy nginx configuration
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
