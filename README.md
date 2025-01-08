# Dynamic Resources POC

# Start postgres database
`./start-postgresql.sh`

# Run
`go run main.go`

# Check db
```sh 
curl -X GET http://localhost:8080/resource-data | jq
```

# API Request

## Add new resource type
```sh
curl --location 'http://localhost:8080/resource-types' \
--header 'Content-Type: application/json' \
--data '{
  "name": "k8s_cluster",
  "schema": {
    "$schema": "http://json-schema.org/draft-07/schema#",
    "type": "object",
    "properties": {
      "external_cluster_id": { "type": "integer" },
      "cluster_status": { "type": "string" }
    },
    "required": ["external_cluster_id", "cluster_status"]
  }
}

'
```

or alternatively use a JSON payload

```sh
curl --location 'http://localhost:8080/resource-types' \
--header 'Content-Type: application/json' \
--data @data/resource_types/k8s_cluster.json
```

# Create Resource

```sh
curl --location 'http://localhost:8080/resource-data/k8s_cluster' \
--header 'Content-Type: application/json' \
--data '{
  "external_cluster_id": 123,
  "cluster_status": "READY"
}'
```

or alternatively use a JSON payload

```sh
curl --location 'http://localhost:8080/resource-data/k8s_cluster' \
--header 'Content-Type: application/json' \
--data @data/resource_data/k8s_cluster.json
```

# Stop postgres database
`./stop-postgresql.sh`