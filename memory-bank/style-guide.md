# Style Guide - Greedy Eye

## Go Code Standards

### General Principles
- Follow effective Go practices and idioms
- Prioritize readability and maintainability over clever solutions
- Keep functions small and focused on single responsibility
- Use clear, descriptive names for variables, functions, and types
- Write self-documenting code with minimal but meaningful comments

### Naming Conventions
- Variables: camelCase (e.g., portfolioValue, assetPrice)
- Functions: PascalCase for exported, camelCase for private
- Types: PascalCase (e.g., AssetService, PriceData)
- Constants: UPPER_SNAKE_CASE (e.g., MAX_RETRY_ATTEMPTS)
- Packages: lowercase, single word when possible

### Error Handling
- Always handle errors explicitly
- Use wrapped errors for context
- Return errors as the last return value
- Use custom error types for business logic errors
- Log errors at the appropriate level

### Function Design
- Keep functions under 50 lines when possible
- Use early returns to reduce nesting
- Group parameters logically
- Use context.Context for cancellation and timeouts
- Return concrete types, accept interfaces

## Testing Standards

### Unit Tests
- One test file per source file
- Use table-driven tests for multiple scenarios
- Include both positive and negative test cases
- Use descriptive test names
- Mock external dependencies

### Integration Tests
- Test complete workflows
- Use Docker containers for dependencies
- Clean up resources after tests
- Use realistic test data
- Test error scenarios

## Security Standards

### API Key Management
- Never commit API keys to version control
- Use environment variables for secrets
- Implement key rotation procedures
- Use least privilege access
- Monitor API key usage

### Input Validation
- Validate all user inputs
- Use parameterized queries
- Implement rate limiting
- Sanitize outputs
- Use HTTPS for all communications

## Performance Standards

### Database Operations
- Use appropriate indexes
- Implement connection pooling
- Use batch operations for bulk data
- Monitor query performance
- Implement caching where appropriate

### API Performance
- Implement request timeouts
- Use connection pooling
- Implement circuit breakers
- Monitor response times
- Use appropriate pagination
