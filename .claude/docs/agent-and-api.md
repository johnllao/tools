# Creating AI Agents — API Reference

Covers DeepSeek, Kimi (Moonshot AI), and Claude/Anthropic patterns for building AI agents with tool calling.

---

## DeepSeek

DeepSeek uses an **OpenAI-compatible API** at `https://api.deepseek.com`. Use the standard OpenAI SDK with DeepSeek as the backend. Also exposes an **Anthropic-compatible endpoint** at `https://api.deepseek.com/anthropic`.

### Models

| Model | Description |
|---|---|
| `deepseek-v4-pro` | Full V4 with function calling + reasoning |
| `deepseek-v4-flash` | Fast, cheap workhorse for routine agent tasks |
| `deepseek-chat` | Legacy alias → v4-flash (deprecated 2026-07-24) |
| `deepseek-reasoner` | Legacy R1 reasoning (no native function calling) |

### Basic Setup

```python
# pip install openai
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["DEEPSEEK_API_KEY"],
    base_url="https://api.deepseek.com",
)

response = client.chat.completions.create(
    model="deepseek-v4-pro",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)
```

```javascript
// npm install openai
import OpenAI from "openai";

const openai = new OpenAI({
    baseURL: "https://api.deepseek.com",
    apiKey: process.env.DEEPSEEK_API_KEY,
});

async function main() {
    const completion = await openai.chat.completions.create({
        messages: [{ role: "user", content: "Hello!" }],
        model: "deepseek-v4-pro",
    });
    console.log(completion.choices[0].message.content);
}
main();
```

```bash
curl https://api.deepseek.com/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${DEEPSEEK_API_KEY}" \
  -d '{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"Hello!"}]}'
```

### Thinking Mode

```python
response = client.chat.completions.create(
    model="deepseek-v4-pro",
    messages=[{"role": "user", "content": "Solve this..."}],
    reasoning_effort="high",
    extra_body={"thinking": {"type": "enabled"}},
)
```

**Important:** In multi-turn conversations with thinking mode, pass back the `reasoning_content` field from previous assistant messages, or the API returns a 400 error.

### Single-Round Tool Calling

```python
from openai import OpenAI

client = OpenAI(api_key="...", base_url="https://api.deepseek.com")

tools = [
    {
        "type": "function",
        "function": {
            "name": "get_weather",
            "description": "Get weather of a location",
            "parameters": {
                "type": "object",
                "properties": {
                    "location": {"type": "string", "description": "City and state, e.g. San Francisco, CA"}
                },
                "required": ["location"],
            },
        },
    },
]

messages = [{"role": "user", "content": "How's the weather in Hangzhou?"}]

response = client.chat.completions.create(
    model="deepseek-v4-pro", messages=messages, tools=tools,
)

message = response.choices[0].message
tool = message.tool_calls[0]

messages.append(message)
messages.append({"role": "tool", "tool_call_id": tool.id, "content": "24℃"})

response = client.chat.completions.create(
    model="deepseek-v4-pro", messages=messages, tools=tools,
)
print(response.choices[0].message.content)
# → "The weather in Hangzhou is currently 24℃."
```

### Complete Agent Loop (with max_turns guard)

```python
import json
from openai import OpenAI

TOOL_MAP = {
    "get_weather": lambda location: f"24℃, sunny in {location}",
    "search_web": lambda query: f'Top result for "{query}": ...',
}

TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "get_weather",
            "description": "Get weather of a location",
            "parameters": {
                "type": "object",
                "properties": {"location": {"type": "string"}},
                "required": ["location"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "search_web",
            "description": "Search the web",
            "parameters": {
                "type": "object",
                "properties": {"query": {"type": "string"}},
                "required": ["query"],
            },
        },
    },
]

def run_agent(user_input: str, max_turns: int = 10) -> str:
    client = OpenAI(api_key="...", base_url="https://api.deepseek.com")
    messages = [
        {"role": "system", "content": "You are a helpful assistant. Use tools when needed."},
        {"role": "user", "content": user_input},
    ]

    for turn in range(max_turns):
        response = client.chat.completions.create(
            model="deepseek-v4-pro", messages=messages, tools=TOOLS,
        )
        msg = response.choices[0].message
        messages.append(msg.model_dump(exclude_none=True))

        if not msg.tool_calls:
            return msg.content  # done

        for tc in msg.tool_calls:
            name = tc.function.name
            args = json.loads(tc.function.arguments)
            print(f"  🔧 [{turn+1}] {name}({args})")
            try:
                result = TOOL_MAP[name](**args)
            except Exception as e:
                result = f"Error: {e}"
            messages.append({
                "role": "tool", "tool_call_id": tc.id, "content": result,
            })

    raise RuntimeError(f"Agent exceeded {max_turns} turns")

print(run_agent("What's the weather in Tokyo?"))
```

### Production-Grade Agent (async, retries, class-based)

```python
import json, asyncio
from openai import AsyncOpenAI

class DeepSeekAgent:
    def __init__(self, api_key: str, tools: list[dict], tool_map: dict):
        self.client = AsyncOpenAI(api_key=api_key, base_url="https://api.deepseek.com")
        self.tools = tools
        self.tool_map = tool_map

    async def _call_with_retry(self, messages, max_retries=3):
        for attempt in range(max_retries):
            try:
                return await self.client.chat.completions.create(
                    model="deepseek-v4-pro", messages=messages, tools=self.tools,
                )
            except Exception as e:
                if attempt == max_retries - 1:
                    raise
                await asyncio.sleep(2 ** attempt)

    async def run(self, user_input: str, max_turns: int = 10) -> str:
        messages = [
            {"role": "system", "content": "You are a helpful assistant."},
            {"role": "user", "content": user_input},
        ]
        for turn in range(max_turns):
            response = await self._call_with_retry(messages)
            msg = response.choices[0].message
            messages.append(msg.model_dump(exclude_none=True))

            if not msg.tool_calls:
                return msg.content

            for tc in msg.tool_calls:
                name = tc.function.name
                args = json.loads(tc.function.arguments)
                try:
                    result = self.tool_map[name](**args)
                except Exception as e:
                    result = f"ToolError: {e}"
                messages.append({
                    "role": "tool", "tool_call_id": tc.id,
                    "content": json.dumps(result, ensure_ascii=False),
                })
        return f"⚠️ Max turns ({max_turns}) reached."

# Usage
async def main():
    agent = DeepSeekAgent(api_key="...", tools=TOOLS, tool_map=TOOL_MAP)
    print(await agent.run("What's 256 * 4096?"))
asyncio.run(main())
```

### Multi-Agent: Planner-Executor Split

DeepSeek-R1 (`deepseek-reasoner`) doesn't support function calling natively — split planning from execution:

```python
# Planner (no tools — pure reasoning)
plan = client.chat.completions.create(
    model="deepseek-reasoner",
    messages=[{"role": "user", "content": "Plan how to research topic X"}],
)

# Executor (with tools)
result = run_agent(f"Execute this plan: {plan.choices[0].message.content}")
```

### DeepSeek Key Takeaways

| Concept | Practice |
|---|---|
| **Client** | OpenAI SDK with `base_url="https://api.deepseek.com"` |
| **Models** | `deepseek-v4-pro` (reasoning), `deepseek-v4-flash` (fast) |
| **Tool schema** | Standard OpenAI `{"type": "function", "function": {...}}` |
| **Loop guard** | Cap with `max_turns` (5–10) to prevent infinite loops |
| **Error handling** | Return errors as tool result strings — model self-corrects |
| **Thinking mode** | Pass back `reasoning_content` in subsequent requests |
| **Strict mode** | Beta: `base_url="https://api.deepseek.com/beta"` with `"strict": true` |
| **Anthropic compat** | `https://api.deepseek.com/anthropic` for Anthropic SDKs |

### Sources

- [DeepSeek API Docs — Tool Calls](https://api-docs.deepseek.com/guides/tool_calls/) — official function calling guide
- [DeepSeek Quick Start](https://api-docs.deepseek.com/quick_start/overview) — client setup and model listing
- [deepseek-go SDK](https://github.com/cohesion-org/deepseek-go) — Go client with function calling examples
- [deepseek-as-subagent](https://github.com/PsChina/deepseek-as-subagent) — production-grade agent loop with MCP tool integration
- [DeepSeek V4 Agent Guide (Tencent Cloud)](https://cloud.tencent.com.cn/developer/article/2681904) — async agent with tool executor, Python sandbox
- [OpenAI Agents SDK + DeepSeek (Qiniu)](https://developer.qiniu.com/aitokenapi/13404/new-deepseek-with-openai-sdk-build-agent) — multi-agent orchestration with planning/dispatcher/reflection agents
- [My DeepSeek Agent Stack in 2026](https://dev.to/gentleforge/my-deepseek-agent-stack-in-2026-a-freedom-first-guide-3ohj) — freedom-first stack guide
- [ARPAHLS/skillware — DeepSeek integration](https://github.com/ARPAHLS/skillware/blob/main/docs/usage/deepseek.md) — skill-based agent with tool loop
- [Docker Agent — DeepSeek provider](https://docs.docker.com/ai/docker-agent/providers/deepseek/) — YAML-configured DeepSeek agents in Docker
- [Swarms Framework — DeepSeek agents](https://docs.swarms.world/examples/model-providers/deepseek) — multi-agent swarms with streaming, memory, context compression
- [DeepSeek V3.2 Math & Physics Agent (GetStream)](https://getstream.io/blog/math-physics-agent-deepseek/) — voice-enabled tutor with WebRTC
- [OpenBrowser AI — deepseek-chat example](https://github.com/billy-enrizky/openbrowser-ai/blob/main/examples/models/deepseek-chat.py) — browser automation agent
- [AgentScope Java — DeepSeek](https://developer.aliyun.com/article/1740717) — Java agent framework with thinking mode and session management

---

## Kimi (Moonshot AI)

Kimi exposes an OpenAI-compatible API at `https://api.moonshot.cn/v1`, so you can build agents using the standard `openai` Python client or any HTTP client that speaks the same protocol.

## 1. Basic Chat Agent

```python
from openai import OpenAI

client = OpenAI(
    api_key="YOUR_KIMI_API_KEY",
    base_url="https://api.moonshot.cn/v1"
)

response = client.chat.completions.create(
    model="kimi-k2-0711-preview",  # or kimi-k1.5, etc.
    messages=[
        {"role": "system", "content": "You are a helpful coding assistant."},
        {"role": "user", "content": "Explain recursion in Python."}
    ]
)
print(response.choices[0].message.content)
```

## 2. Tool-Using Agent

Define tools and let Kimi decide when to call them.

```python
from openai import OpenAI
import json

client = OpenAI(api_key="...", base_url="https://api.moonshot.cn/v1")

tools = [
    {
        "type": "function",
        "function": {
            "name": "get_weather",
            "description": "Get current weather for a city",
            "parameters": {
                "type": "object",
                "properties": {
                    "city": {"type": "string"}
                },
                "required": ["city"]
            }
        }
    }
]


def get_weather(city: str):
    return {"city": city, "temperature": 22, "condition": "sunny"}


messages = [
    {"role": "system", "content": "Use available tools when needed."},
    {"role": "user", "content": "What's the weather in Beijing?"}
]

response = client.chat.completions.create(
    model="kimi-k2-0711-preview",
    messages=messages,
    tools=tools
)

tool_call = response.choices[0].message.tool_calls[0]
args = json.loads(tool_call.function.arguments)
result = get_weather(**args)

messages.append(response.choices[0].message)
messages.append({
    "role": "tool",
    "tool_call_id": tool_call.id,
    "content": json.dumps(result)
})

final = client.chat.completions.create(
    model="kimi-k2-0711-preview",
    messages=messages,
    tools=tools
)
print(final.choices[0].message.content)
```

## 3. ReAct-Style Reasoning Agent

A simple loop: think, act, observe.

```python
from openai import OpenAI
import json

client = OpenAI(api_key="...", base_url="https://api.moonshot.cn/v1")


def calculate(expression: str):
    return {"result": eval(expression)}


def react_agent(question, tools, max_steps=5):
    messages = [
        {"role": "system", "content": "Answer by thinking step by step. Use tools when needed."},
        {"role": "user", "content": question}
    ]

    for _ in range(max_steps):
        response = client.chat.completions.create(
            model="kimi-k2-0711-preview",
            messages=messages,
            tools=tools
        )
        msg = response.choices[0].message

        if msg.content:
            print("Reasoning:", msg.content)

        if not msg.tool_calls:
            return msg.content

        for tc in msg.tool_calls:
            args = json.loads(tc.function.arguments)
            result = globals()[tc.function.name](**args)
            messages.append(msg)
            messages.append({
                "role": "tool",
                "tool_call_id": tc.id,
                "content": json.dumps(result)
            })


math_tools = [{
    "type": "function",
    "function": {
        "name": "calculate",
        "description": "Evaluate a math expression",
        "parameters": {
            "type": "object",
            "properties": {
                "expression": {"type": "string"}
            },
            "required": ["expression"]
        }
    }
}]

print(react_agent("What is 15 * 27?", tools=math_tools))
```

## 4. Multi-Agent System

Route tasks between specialized agents.

```python
class Agent:
    def __init__(self, name, system_prompt, model="kimi-k2-0711-preview"):
        self.name = name
        self.system = system_prompt
        self.model = model
        self.client = OpenAI(api_key="...", base_url="https://api.moonshot.cn/v1")

    def run(self, task):
        response = self.client.chat.completions.create(
            model=self.model,
            messages=[
                {"role": "system", "content": self.system},
                {"role": "user", "content": task}
            ]
        )
        return response.choices[0].message.content


planner = Agent("planner", "Break tasks into step-by-step plans.")
coder = Agent("coder", "Write clean, working Python code.")
reviewer = Agent("reviewer", "Review code for bugs and style issues.")

task = "Build a script that fetches a webpage title"

plan = planner.run(task)
code = coder.run(f"Task: {task}\nPlan: {plan}")
review = reviewer.run(f"Code:\n{code}")

print(f"--- Plan ---\n{plan}\n--- Code ---\n{code}\n--- Review ---\n{review}")
```

## 5. Document / RAG Agent

Kimi models support very long contexts, which makes them well suited for document-based agents.

```python
def document_agent(document, question):
    client = OpenAI(api_key="...", base_url="https://api.moonshot.cn/v1")

    response = client.chat.completions.create(
        model="kimi-k2-0711-preview",
        messages=[
            {"role": "system", "content": "Answer based only on the provided document."},
            {"role": "user", "content": f"Document:\n{document}\n\nQuestion: {question}"}
        ]
    )
    return response.choices[0].message.content
```

## Key Tips

- **Base URL**: `https://api.moonshot.cn/v1`
- **Models**: check Moonshot's docs for current model names such as `kimi-k2-0711-preview` or `kimi-k1.5`.
- **Tool calling**: uses the same schema as OpenAI function calling.
- **Streaming**: add `stream=True` for real-time token streaming.
- **Long context**: Kimi models support large context windows, making them useful for RAG over long documents.
