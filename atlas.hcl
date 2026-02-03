variable "database_url" {
  type    = string
  default = getenv("EYE_DB_URL")
}

env "local" {
  src = "file://schema.hcl"
  url = var.database_url
  dev = "docker://postgres/17/greedy_eye?search_path=public"
}

env "docker" {
  src = "file://schema.hcl"
  url = var.database_url
  dev = "docker://postgres/17/greedy_eye?search_path=public"
}

env "test" {
  src = "file://schema.hcl"
  url = var.database_url
}
