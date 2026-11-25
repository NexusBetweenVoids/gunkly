# AI chore suggestions

Optional and off by default. When it is on, people can describe chores in their own words, for example "vacuum the living room every Saturday at 10, Sam and Alex take turns doing the dishes, descale the kettle every 3 months", and Gunkly proposes chores. They check each one and add it as it is, or open it in the normal form to change it first. Nothing is created until they do.

This is separate from [connecting an AI assistant](ai-assistants.md): there, your own assistant (Claude, ChatGPT, …) talks to Gunkly. Here, Gunkly talks to an AI model that the admin chose.

## For admins: set it up

Gunkly works with any service that speaks the OpenAI chat API, so you decide whether to use a paid service or a model you run yourself.

1. Open **Administration**, **AI**.
2. Fill in:
   - **API address**: the base address ending in `/v1`.
     - OpenAI: `https://api.openai.com/v1`; other providers (Mistral, OpenRouter, Groq, …) give theirs in their docs.
     - Ollama: `http://<host>:11434/v1`. LM Studio: `http://<host>:1234/v1`. llama.cpp server and vLLM: their address followed by `/v1`.
   - **Model**: the model's name as the service knows it, for example `gpt-4o-mini` or `llama3.1`. A small model is usually enough: the task is short and structured.
   - **API key**: from your account at the provider. A local model usually needs none.
3. Tick **Turn on AI chore suggestions** and press **Save**, then **Test connection**.

People then see **Describe your chores (AI)** at the top of **Add chore**.

You can also set it in the server's environment (`GUNKLY_AI_ENABLED`, `GUNKLY_AI_URL`, `GUNKLY_AI_MODEL`, `GUNKLY_AI_KEY`). A variable that is set always wins: its field in Administration is then locked.

**Running Gunkly in Docker and the model on the same machine?** `localhost` inside the Gunkly container is the container itself. Use the model's container name on a shared Docker network (`http://ollama:11434/v1`), or the host's LAN address.

### Good to know

- **Addresses on your own network are allowed** for this setting, so a model next to Gunkly works. Cloud metadata and link-local addresses (`169.254.x.x`) are always blocked, and Gunkly never follows redirects from the AI endpoint.
- **The key is stored encrypted** with your `GUNKLY_SECRET_KEYS` and is never shown again or written to logs. `gunkly rotate-key` re-encrypts it.
- **Costs:** each person can ask 50 times per 24 hours by default (Administration, **Limits**, "AI chore suggestions per user per day"); at most 4 requests run at the same time. The counter starts again when Gunkly restarts.
- **Slow models:** Gunkly waits up to two minutes for an answer.
- Guests cannot use it, and API tokens cannot reach it.

## What is sent to the model

For each request: the text the person typed, the names of the household's areas (when areas are on), the display names of members who are not guests, and today's date. Nothing else: no email addresses, no other chores, no history, nothing from other households.

With a paid service this goes to that company under its terms. With a model on your own network it stays there.

## What the model can and cannot do

The model only proposes. Its answer is checked like anything typed by hand: names, schedules, dates and times must be valid, assignees and areas must already exist in the household, and anything else is left out. The person sees every proposal before it is added. The model cannot create, change or delete anything by itself, and text that tries to give it instructions is treated as a chore description.

## Troubleshooting

**Test connection** says what went wrong:

- **Can't reach that address:** the service is not running, the address is wrong, or Gunkly's container cannot reach it (see the Docker note above).
- **The service refused the key:** check the API key.
- **Nothing found there:** the address should end in `/v1`, and the model name must exist (`ollama list` shows local ones).
- **No answer in time:** a local model may still be loading; try again in a minute.
- **Not like an OpenAI-compatible API:** the address points at something else, such as a web page.
