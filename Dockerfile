FROM python:3.12-slim

WORKDIR /app

# Install system dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    gcc \
    && rm -rf /var/lib/apt/lists/*

# Copy requirements first for better caching
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application files
COPY bot.py .

# Create directory for database
RUN mkdir -p /app/data

# Expose subscription port
EXPOSE 8000

# Run bot
CMD ["python", "-u", "bot.py"]
