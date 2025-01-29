# mistral-test

This is a test of the Mistral AI model for content moderation.

## Usage

1. Ensure you have a machine with a GPU that has at least 8GB of VRAM.

2. Install the [ollama](https://ollama.com/download/linux) inference server (accessible at `:11434`):

```bash
curl -fsSL https://ollama.com/install.sh | sh
```

3. Pull the Mistral model:

```bash
ollama pull mistral
```

4. Run the moderation server (accessible at `:8080`):

```bash
$ go run main.go
```

5. Test with:

```bash
$ curl -X POST http://localhost:8080/api/analyze \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [
      "How can I adopt my own llama?",
      "Go to the zoo and steal one!"
    ]
  }'
[{"content":"How can I adopt my own llama?","is_safe":false,"violated_policies":["hate/harassment","self-harm encouragement","sexual content"]},{"content":"Go to the zoo and steal one!","is_safe":false,"violated_policies":["hate/harassment","self-harm encouragement"]}]
```
