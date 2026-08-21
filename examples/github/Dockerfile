# Provisioner service — extends the base image with this repo's services.txt.
# Railway builds from this Dockerfile when the service is connected to your repo.
FROM ghcr.io/pecatatoshev/railway-db-provisioner:latest

ENV SERVICES_FILE=/app/services.txt
COPY services.txt /app/services.txt
