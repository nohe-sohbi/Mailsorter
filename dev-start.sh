#!/bin/bash

# Mailsorter - Development startup script

echo "Starting Mailsorter development environment..."

# Check if .env file exists
if [ ! -f .env ]; then
    echo "Creating .env file from template..."
    cp .env.example .env
    echo "Please edit .env file with your Gmail API credentials before continuing."
    exit 1
fi

# Start MongoDB using Docker
echo "Starting MongoDB..."
docker run -d \
    --name mailsorter-mongodb-dev \
    -p 27017:27017 \
    -e MONGO_INITDB_ROOT_USERNAME=admin \
    -e MONGO_INITDB_ROOT_PASSWORD=password \
    -e MONGO_INITDB_DATABASE=mailsorter \
    -v $(pwd)/mongo-init:/docker-entrypoint-initdb.d \
    mongo:7.0

# Wait for MongoDB to be ready
echo "Waiting for MongoDB to be ready..."
sleep 5

# Start backend
echo "Starting Go backend..."
cd backend
go run cmd/server/main.go &
BACKEND_PID=$!
cd ..

# Start frontend
echo "Starting React frontend..."
cd frontend
npm install
npm start &
FRONTEND_PID=$!
cd ..

echo ""
echo "===================================="
echo "Mailsorter is running!"
echo "===================================="
echo "Frontend: http://localhost:3000"
echo "Backend: http://localhost:8080"
echo "MongoDB: localhost:27017"
echo ""
echo "Press Ctrl+C to stop all services"
echo ""

# Trap Ctrl+C and cleanup
trap "echo 'Stopping services...'; kill $BACKEND_PID $FRONTEND_PID; docker stop mailsorter-mongodb-dev; docker rm mailsorter-mongodb-dev; exit" INT

# Wait
wait
