# CodeCTX

CodeCTX is an AI-powered code context analysis tool that helps understand codebases by analyzing their structure and providing intelligent insights.

## Components

- **Client**: A Go application that analyzes code structure and sends context to the server
- **Server**: A Go application that processes code context using Google's Generative AI (Gemini)

## Installation

1. Clone the repository
2. Install Go 1.23.4 or later

## Usage

1. Start the server:

```bash
cd apps/server
make build
./server
```

2. Start the client:

```bash
cd apps/client
make build
./client
```

3. Server will write `code.ctx` for debugging purposes.

4. Provide a client prompt and wait for server response.

## Features

- Analyzes code structure using tree-sitter
- Supports multiple languages including Go, JavaScript, TypeScript, and Python
- Real-time code analysis with AI-powered insights
- Interactive command-line interface

## Configuration

- Set log level using environment variable: `CTX_LOG=[debug|trace|error|info]`
- Configure file ignoring patterns in `.ctxignore`

## Contributing

1. Fork the repository
2. Create your feature branch
3. Make your changes
4. Submit a pull request
