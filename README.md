# bitmap-leaser

Playground program to test bitmap algorithms.

Featured in blog post: https://jmpargana.com/posts/bitmap

## Usage

```bash
make test   # Run tests
make build  # Compile binary
make run    # Build and run server
make clean  # Remove binary
```

Server starts on `:8080` with endpoints:
- `GET /health` - Liveness check
- `GET /ip` - Allocate IP
- `PUT /ip/{ip}/renew` - Extend lease TTL
- `DELETE /ip/{ip}` - Release IP
