
# Describe the Docker environment.
env "docker" {
  # Path to Ent schema directory.
  src = "ent://internal/services/storage/ent/schema"

  # Get the database URL from environment variable in docker-compose.yml.
  url = getenv("EYE_DB_URL")

  # Specify the migration directory for this environment.
  migration {
    dir = "file://deploy/migrations"
  }

  dev = "docker://postgres/17/greedy_eye?search_path=public&sslmode=disable"
}
