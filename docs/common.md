# Common notices and cheat sheets

## Money amount and precision

Common practice is to use decimal types to store amount with precision.

  real_value = amount / 10^precision

Transaction, price, holding and other entities can have this fields.

## Dev cheat sheets

### Run golangci-lint locally

    docker run --rm -v (pwd):/app -w /app golangci/golangci-lint:latest golangci-lint run -v

### Push to public dockr registry
