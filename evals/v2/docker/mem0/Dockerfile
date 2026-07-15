ARG MEM0_BASE_IMAGE=mem0/mem0-api-server@sha256:2fcf4bb713cfc584d454bf06993cc7e2fe51540695995bd3aa9c7008b7065c75
FROM ${MEM0_BASE_IMAGE}

RUN python -m pip install --no-cache-dir "psycopg[binary]==3.3.4" "psycopg-pool==3.3.1"
RUN sed -i '/^    "graph_store": {$/,/^    },$/d' /app/main.py \
    && ! grep -q '^    "graph_store": {$' /app/main.py
