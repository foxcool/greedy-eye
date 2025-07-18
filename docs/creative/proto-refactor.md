# Proto Refactor Strategy

## Problem Statement

Current proto structure has issues:

- Generated files scattered between `api/` and `internal/api/`
- Complex rule schemas with premature optimization
- Over-engineered error handling types
- Build complexity with multiple generation paths

## Decision

**Selected Approach**: Unified generation path with simplified schemas

**Rationale**:
1. Single canonical location for generated Go files
2. YAGNI principle - avoid premature optimization
3. Simplified build process
4. Better maintainability

## Implementation

### Generation Path Changes

Update `buf.gen.yaml` and `buf.gen.gateway.yaml`:

```yaml
plugins:
  - plugin: buf.build/protocolbuffers/go
    out: internal/api
    opt:
      - paths=source_relative
  - plugin: buf.build/grpc/go
    out: internal/api
    opt:
      - paths=source_relative
      - require_unimplemented_servers=false
```

### Schema Simplification

#### Rule Schema (Simplified)
```proto
// api/models/rule.proto
message Rule {
  string id = 1;
  string name = 2;
  string description = 3;
  RuleType type = 4;
  google.protobuf.Struct configuration = 5;  // Flexible config
  bool enabled = 6;
  google.protobuf.Timestamp created_at = 7;
  google.protobuf.Timestamp updated_at = 8;
}

enum RuleType {
  RULE_TYPE_UNSPECIFIED = 0;
  RULE_TYPE_TARGET_ALLOCATION = 1;
  RULE_TYPE_MONTHLY_WITHDRAWAL = 2;
  RULE_TYPE_STOP_LOSS = 3;
  RULE_TYPE_DCA = 4;
}
```

#### Rule Execution Schema (Simplified)
```proto
// api/models/rule_execution.proto
message RuleExecution {
  string id = 1;
  string rule_id = 2;
  ExecutionStatus status = 3;
  google.protobuf.Timestamp started_at = 4;
  google.protobuf.Timestamp completed_at = 5;
  google.protobuf.Struct summary = 6;  // Flexible summary
  string error_message = 7;
}

enum ExecutionStatus {
  EXECUTION_STATUS_UNSPECIFIED = 0;
  EXECUTION_STATUS_PENDING = 1;
  EXECUTION_STATUS_RUNNING = 2;
  EXECUTION_STATUS_COMPLETED = 3;
  EXECUTION_STATUS_FAILED = 4;
}
```

#### Error Details Schema (Minimal)
```proto
// api/models/error_details.proto
message ErrorDetails {
  string error_code = 1;
  string message = 2;
  map<string, string> metadata = 3;
}
```

## Migration Steps

1. **Update buf configuration**: Modify generation output to `internal/api`
2. **Delete generated files**: Remove all `*.pb.go` files from `api/`
3. **Regenerate**: Run `buf generate` to create files in new location
4. **Update imports**: Change import paths to `internal/api/`
5. **Simplify schemas**: Remove complex typed messages
6. **Test compilation**: Ensure all services compile successfully

## Benefits

1. **Cleaner structure**: Single location for generated files
2. **Reduced complexity**: Simpler proto schemas
3. **Faster builds**: Less code to compile
4. **Better maintainability**: Easier to understand and modify
5. **Flexibility**: Struct-based configuration allows future changes without breaking compatibility

## Verification

- [ ] Buf generates into `internal/api/**` only
- [ ] `go vet ./...` passes
- [ ] `go test ./...` passes
- [ ] No duplicate generated files in repo
- [ ] Protos compile with simplified schemas