# Juno RPC Proxy

A secure RPC proxy for Juno Cash nodes that provides authentication-free access for miners while protecting your node credentials. Also includes an optional ZMQ notification proxy for distributing block notifications to multiple miners.

## Features

- **RPC Method Whitelist**: Only allows specified RPC methods, blocking potentially dangerous calls
- **Credential Isolation**: Miners connect without credentials; proxy handles upstream authentication
- **ZMQ Proxy**: Forwards block notifications from node to multiple miners (optional)
- **Configurable Authentication**: Optional authentication for miners connecting to the proxy

## Requirements

- Go 1.21 or higher
- libzmq development libraries (for ZMQ proxy feature)

### Install Dependencies (Ubuntu/Debian)

```bash
sudo apt-get install -y libzmq3-dev
```

### Install Dependencies (CentOS/RHEL)

```bash
sudo yum install -y zeromq-devel
```

### Install Dependencies (Windows)

1. Install [Go](https://go.dev/dl/) (1.21 or higher)
2. Install [MSYS2](https://www.msys2.org/)
3. Open MSYS2 MinGW64 terminal and install ZMQ:
   ```bash
   pacman -S mingw-w64-x86_64-zeromq
   ```

## Building

### Linux/macOS

```bash
go build -o juno-proxy
```

### Windows

Using MSYS2 MinGW64 terminal:

```bash
go build -o juno-proxy.exe
```

Or using PowerShell with CGO enabled:

```powershell
$env:CGO_ENABLED = "1"
go build -o juno-proxy.exe
```

Note: The ZMQ library requires CGO. Ensure your PATH includes the MinGW64 bin directory (e.g., `C:\msys64\mingw64\bin`) for the required DLLs at runtime.

## Configuration

Copy the example configuration file and edit it:

```bash
cp config.example.toml config.toml
```

### Basic Configuration

```toml
# Address and port to listen on
listen = "0.0.0.0:8233"

# RPC method whitelist - only these methods will be proxied
allowed_methods = [
    "getblocktemplate",
    "submitblock",
    "getblockchaininfo",
    "getmininginfo",
    "getblockhash",
    "getblock",
    "getbestblockhash",
]

# Upstream junocash node connection
[upstream]
url = "http://127.0.0.1:8232"
username = "rpcuser"
password = "rpcpassword"
timeout = "30s"
```

### Proxy Authentication (Optional)

Enable authentication for miners connecting to this proxy if it's exposed to untrusted networks:

```toml
[proxy_auth]
enabled = true
username = "miner"
password = "minerpassword"
```

### ZMQ Proxy Configuration

The ZMQ proxy feature forwards block notifications from your Juno Cash node to multiple miners. This is useful when:

- Running multiple miners that need instant block notifications
- Your node is on a different machine than your miners
- You want to reduce the number of ZMQ connections to your node

#### Node Configuration (junocashd.conf)

First, enable ZMQ notifications on your Juno Cash node. Add to `~/.junocash/junocashd.conf`:

```
zmqpubhashblock=tcp://127.0.0.1:28332
```

Restart junocashd after making this change.

#### Proxy Configuration

Enable the ZMQ proxy in your `config.toml`:

```toml
[zmq]
enabled = true
upstream_url = "tcp://127.0.0.1:28332"  # Your node's ZMQ address
listen = "tcp://0.0.0.0:28333"          # Address miners connect to
topic = "hashblock"                      # Topic to forward (default)
```

| Option | Description |
|--------|-------------|
| `enabled` | Set to `true` to enable the ZMQ proxy |
| `upstream_url` | The ZMQ endpoint of your Juno Cash node |
| `listen` | The address where miners will connect for ZMQ notifications |
| `topic` | The ZMQ topic to subscribe/publish (default: `hashblock`) |

#### Miner Configuration

Configure juno-miner to connect to the proxy's ZMQ endpoint instead of the node directly:

```bash
./juno-miner --rpc-url http://proxy-host:8233 --zmq-url tcp://proxy-host:28333
```

## Usage

### Running the Proxy

```bash
./juno-proxy -config config.toml
```

### Command Line Options

- `-config PATH` - Path to configuration file (default: `config.toml`)
- `-version` - Show version and exit

### Example Setup

```
                          ┌─────────────────┐
                          │   junocashd     │
                          │                 │
                          │ RPC: 8232       │
                          │ ZMQ: 28332      │
                          └────────┬────────┘
                                   │
                          ┌────────▼────────┐
                          │   juno-proxy    │
                          │                 │
                          │ RPC: 8233       │
                          │ ZMQ: 28333      │
                          └────────┬────────┘
                                   │
              ┌────────────────────┼────────────────────┐
              │                    │                    │
      ┌───────▼───────┐    ┌───────▼───────┐    ┌───────▼───────┐
      │  juno-miner   │    │  juno-miner   │    │  juno-miner   │
      │   Worker 1    │    │   Worker 2    │    │   Worker 3    │
      └───────────────┘    └───────────────┘    └───────────────┘
```

## Security Notes

- The proxy isolates your node's RPC credentials from miners
- Use `proxy_auth` if the proxy is exposed to untrusted networks
- The method whitelist prevents miners from executing dangerous RPC calls
- Consider firewall rules to restrict access to the proxy

## License

MIT License
