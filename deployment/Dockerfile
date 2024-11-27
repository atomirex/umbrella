# Run Stage
FROM ubuntu:22.04
 
COPY tmp/umbrella umbrella

# Expose application ports
EXPOSE 8081
EXPOSE 9091
EXPOSE 50000-60000
 
# Start app
CMD ./umbrella
