FROM python:3.12-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    iputils-ping \
    openssh-client \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir --break-system-packages pyyaml==6.0.2

WORKDIR /app
COPY mc_wol_proxy.py .

ENV MC_WOL_CONFIG=/config.yml

EXPOSE 25565

CMD ["python3", "-u", "mc_wol_proxy.py"]
