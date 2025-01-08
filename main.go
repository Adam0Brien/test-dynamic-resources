package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"path/filepath"

	"github.com/go-playground/validator/v10"
	"github.com/gorilla/mux"
	"github.com/qri-io/jsonschema"

	_ "github.com/lib/pq"
)

type ResourceType struct {
	ID     int             `json:"id"`
	Name   string          `json:"name" validate:"required"`
	Schema json.RawMessage `json:"schema" validate:"required"` // Raw JSON schema
}

type ResourceData struct {
	ID             int             `json:"id"`
	ResourceTypeID int             `json:"resource_type_id"`
	Data           json.RawMessage `json:"data"`
}

var (
	db       *sql.DB
	validate *validator.Validate
)

const (
	resourceTypesDir = "data/resource_types"
	resourceDataDir  = "data/resource_data"
)

func initDB() {
	var err error
	connStr := "host=0.0.0.0 port=5432 user=postgres password=secret dbname=testdb sslmode=disable"
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS resource_types (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) UNIQUE NOT NULL,
			schema JSONB NOT NULL
		);

		CREATE TABLE IF NOT EXISTS resource_data (
			id SERIAL PRIMARY KEY,
			resource_type_id INT NOT NULL REFERENCES resource_types(id),
			data JSONB NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	log.Println("Database initialized successfully.")
}

func loadResourceTypes() {
	files, err := ioutil.ReadDir(resourceTypesDir)
	if err != nil {
		log.Fatalf("Failed to read resource types directory: %v", err)
	}

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}

		filePath := filepath.Join(resourceTypesDir, file.Name())
		var resourceType ResourceType

		data, err := ioutil.ReadFile(filePath)
		if err != nil {
			log.Printf("Failed to read file '%s': %v", filePath, err)
			continue
		}

		if err := json.Unmarshal(data, &resourceType); err != nil {
			log.Printf("Failed to parse JSON in '%s': %v", filePath, err)
			continue
		}

		// checks if resource type already exists
		var id int
		query := `SELECT id FROM resource_types WHERE name = $1`
		err = db.QueryRow(query, resourceType.Name).Scan(&id)
		if err == sql.ErrNoRows {
			query = `INSERT INTO resource_types (name, schema) VALUES ($1, $2)`
			_, err = db.Exec(query, resourceType.Name, resourceType.Schema)
			if err != nil {
				log.Printf("Failed to insert resource type '%s': %v", resourceType.Name, err)
			} else {
				log.Printf("Inserted resource type: %s", resourceType.Name)
			}
		} else {
			log.Printf("Resource type '%s' already exists", resourceType.Name)
		}
	}
}

func loadResourceData() {
	files, err := ioutil.ReadDir(resourceDataDir)
	if err != nil {
		log.Fatalf("Failed to read resource data directory: %v", err)
	}

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}

		resourceTypeName := file.Name()[:len(file.Name())-len(filepath.Ext(file.Name()))]
		filePath := filepath.Join(resourceDataDir, file.Name())

		// Fetch the resource type ID and schema
		var resourceType ResourceType
		query := `SELECT id, schema FROM resource_types WHERE name = $1`
		err := db.QueryRow(query, resourceTypeName).Scan(&resourceType.ID, &resourceType.Schema)
		if err == sql.ErrNoRows {
			log.Printf("Resource type '%s' not found for file '%s'", resourceTypeName, filePath)
			continue
		} else if err != nil {
			log.Printf("Failed to fetch resource type '%s': %v", resourceTypeName, err)
			continue
		}

		// Read the resource data file
		data, err := ioutil.ReadFile(filePath)
		if err != nil {
			log.Printf("Failed to read file '%s': %v", filePath, err)
			continue
		}

		var resourceData []json.RawMessage
		if err := json.Unmarshal(data, &resourceData); err != nil {
			log.Printf("Failed to parse JSON in '%s': %v", filePath, err)
			continue
		}

		// Validate and insert each entry
		for _, entry := range resourceData {
			if !validateJSONAgainstSchema(resourceType.Schema, entry) {
				log.Printf("Validation failed for resource data in file '%s': %s", filePath, entry)
				continue
			}

			query = `INSERT INTO resource_data (resource_type_id, data) VALUES ($1, $2)`
			_, err := db.Exec(query, resourceType.ID, entry)
			if err != nil {
				log.Printf("Failed to insert resource data for '%s': %v", resourceTypeName, err)
			}
		}

		log.Printf("Loaded and validated resource data from file: %s", filePath)
	}
}

func main() {
	initDB()
	defer db.Close()
	validate = validator.New()
	loadResourceTypes()
	loadResourceData()

	router := mux.NewRouter()
	//router.HandleFunc("/resource-types", createResourceType).Methods("POST")
	//router.HandleFunc("/resource-data/{resource_type_name}", validateAndStoreResourceData).Methods("POST")

	router.HandleFunc("/resource-data", getAllResourceData).Methods("GET")

	log.Println("Starting server on :8080...")
	log.Fatal(http.ListenAndServe(":8080", router))
}

// createResourceType handles adding new resource types
func createResourceType(w http.ResponseWriter, r *http.Request) {
	var resourceType ResourceType
	if err := json.NewDecoder(r.Body).Decode(&resourceType); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if err := validate.Struct(resourceType); err != nil {
		http.Error(w, fmt.Sprintf("Validation error: %v", err), http.StatusBadRequest)
		return
	}

	// Insert into the database
	query := `INSERT INTO resource_types (name, schema) VALUES ($1, $2) RETURNING id`
	err := db.QueryRow(query, resourceType.Name, resourceType.Schema).Scan(&resourceType.ID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to insert resource type: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resourceType)
}

// validateAndStoreResourceData validates incoming data against a stored resource type
func validateAndStoreResourceData(w http.ResponseWriter, r *http.Request) {
	resourceTypeName := mux.Vars(r)["resource_type_name"]

	// Fetch the resource type
	var resourceType ResourceType
	query := `SELECT id, schema FROM resource_types WHERE name = $1`
	err := db.QueryRow(query, resourceTypeName).Scan(&resourceType.ID, &resourceType.Schema)
	if err == sql.ErrNoRows {
		http.Error(w, "Resource type not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, fmt.Sprintf("Database error: %v", err), http.StatusInternalServerError)
		return
	}

	// Parse incoming data
	var incomingData json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&incomingData); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate data against schema
	if !validateJSONAgainstSchema(resourceType.Schema, incomingData) {
		http.Error(w, "Validation failed: data does not conform to schema", http.StatusBadRequest)
		return
	}

	// Insert validated data
	query = `INSERT INTO resource_data (resource_type_id, data) VALUES ($1, $2) RETURNING id`
	var resourceData ResourceData
	err = db.QueryRow(query, resourceType.ID, incomingData).Scan(&resourceData.ID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to insert resource data: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resourceData)
}

type ResourceDataEntry struct {
	ResourceType string          `json:"resource_type"`
	Data         json.RawMessage `json:"data"`
	CreatedAt    string          `json:"created_at"`
}

// getAllResourceData fetches all resource data and returns it as JSON
func getAllResourceData(w http.ResponseWriter, r *http.Request) {
	// Query to fetch all resource data along with resource type names
	query := `
		SELECT rt.name AS resource_type, rd.data, rd.created_at
		FROM resource_data rd
		INNER JOIN resource_types rt ON rd.resource_type_id = rt.id
		ORDER BY rd.created_at DESC
	`

	// Execute the query
	rows, err := db.Query(query)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query resource data: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []ResourceDataEntry

	// Iterate through the results
	for rows.Next() {
		var entry ResourceDataEntry
		if err := rows.Scan(&entry.ResourceType, &entry.Data, &entry.CreatedAt); err != nil {
			http.Error(w, fmt.Sprintf("Failed to parse query results: %v", err), http.StatusInternalServerError)
			return
		}
		results = append(results, entry)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf("Error iterating through rows: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// validateJSONAgainstSchema validates data against a JSON schema
func validateJSONAgainstSchema(schema, data json.RawMessage) bool {
	// Load the schema
	rs := &jsonschema.Schema{}
	if err := json.Unmarshal(schema, rs); err != nil {
		log.Printf("Failed to parse schema: %v", err)
		return false
	}

	if !checkAdditionalProperties(schema) {
		log.Println("Sanity check failed: 'additionalProperties' is not explicitly set to false.")
		return false
	}

	// Validate the data
	errs, err := rs.ValidateBytes(context.Background(), data)
	if err != nil {
		log.Printf("Validation error: %v", err)
		return false
	}

	// Check for validation errors
	if len(errs) > 0 {
		var buf bytes.Buffer
		for _, err := range errs {
			buf.WriteString(err.Error() + "\n")
		}
		log.Printf("Validation failed:\n%s", buf.String())
		return false
	}

	// Validation successful
	return true
}

func checkAdditionalProperties(schema json.RawMessage) bool {
	var schemaMap map[string]interface{}
	if err := json.Unmarshal(schema, &schemaMap); err != nil {
		log.Printf("Failed to parse schema for sanity check: %v", err)
		return false
	}

	// Check the additionalProperties field
	if ap, ok := schemaMap["additionalProperties"]; ok {
		if apBool, isBool := ap.(bool); isBool && !apBool {
			return true // additionalProperties explicitly set to false
		}
	}
	return false
}
