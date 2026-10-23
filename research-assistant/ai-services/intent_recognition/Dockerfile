FROM python:3.12-slim

ARG PIP_INDEX_URL=https://pypi.org/simple
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    INTENT_SERVICE_PORT=8000

WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -i $PIP_INDEX_URL -r requirements.txt
COPY main.py ./

EXPOSE 8000
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
