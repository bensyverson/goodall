# Goodall initial brief

### Overview of Goodall

This is a completely empty directory, which will become the repository for the project Goodall. I want to create a reusable and extensible core Go library that provides the underlying mechanisms for agentic prototyping. What I mean by that, specifically:

1. An agentic loop (LLM conversation with user-definable tools)
2. Usable for any "give an LLM tools" task, even ones which are not chat or conversation-oriented.
3. A set of *optional* extensions or components to make it easier and faster to develop chat-oriented agents

### Use cases

There are two basic use cases that represent our core library consumers:

- People who want to quickly spin up an agentic chat agent prototype (such as a web-based chat, or the Go back-end for a mobile app)
- People who want to build highly customized agents without writing all the boilerplate

Both are equally important. We want to make simple things easy, and complex things possible.

### Product features

We take it for granted that any modern agentic library needs to support these features:

1. Provider-flexible (not necessarily provider-agnostic) core LLM layer. We need to support at a minimum Anthropic’s and OpenRouter’s APIs. Even having just two ensures that our system is built in a way that could support a third easily. Anthropic features the API which is probably furthest from the OpenAI baseline, and supporting OpenRouter means we can use a wide variety of models, including OpenAI.
	- “Supporting” a provider means full support for the features below, including streaming, vision, thinking blocks and tool schemas.
2. Streaming (opt-out). We should assume streaming as a default while allowing a blocking path for those that need it.
3. Other media (including vision). When models are capable of accepting other types of input such as audio, images, PDFs or beyond, we should enable library consumers to pass those data types in. It may make sense to create ways to enforce model capabilities through type safety. We also need to provide clear feedback from API error messages, as some OpenRouter providers don’t provide vision even when the model itself does.
4. Thinking. We should allow consumers to determine thinking level, ideally in a type-safe way. We should return thinking blocks with responses (including streaming), so the user can introspect the thinking live, or display thinking within their interface.
5. Tool definition. We should provide a simple way to define tools which can be used by any provider. This may require us to pick one schema to standardize on.
6. Stopping. We need to be able to stop and resume generation at will.
7. Cache control. For chat-based consumers, we should provide automatic cache control based on best practices. For customizers, we should allow them to opt out of cache control, or decide where to set cache points.
8. Basic Markdown parsing for output blocks in the chat-based path; users should be able to choose whether to receive the raw result or the parsed HTML (or both should be accessible in the result).
9. Token and cost tracking. By paying attention to API responses, we should help the user track the token use and spend for a given thread.
10. Idea (not set in stone): Prompt and tool result redaction as a default for the chat-based helpers. The idea here is that the front-end needs the conversation history to render its UI, but we should not necessarily expose our prompt or tool results to front-end users, and it’s not necessary for most UIs. So we have a mechanism to redact some elements to uniqueIDs (e.g. 7-char Base62 strings) when sending to the front-end, and re-associating them before posting to the LLM API (to ensure our thread stays cacheable).
11. Hooks / middleware. Not relevant to our basic chat user, but our customizer may want hooks to do things like:
	- Intercept and potentially rewrite user messages before requesting a response from the LLM, so we can e.g. trigger a RAG query or memory search
	- Observe responses from the LLM so we can e.g. scan for secrets or send the response to a moderation API

### Prior art & inspiration

We have many of these pieces already, albeit in Swift form. The sibling repo LLM (../LLM/) is a multi-provider LLM API wrapper, and Operator (../Operator/) is just the agentic portion.

I’m open to following the same pattern (a pure LLM library, and an agent-focused library), but I think there’s enough crossover and shared concerns that a single library could make more sense.

### Our library

I would like our library to feature minimal (ideally zero) dependencies, though we can bring something in if there’s a strong case for it. We want to follow modern Go 1.27 idioms and patterns. If there’s a newer, clearer language feature that can help us in a given situation, let’s adopt it. But let’s also not go wild with abstraction; ideally we can keep this codebase relatively small and tractable.

Given how much JSON wrangling and API differences we’ll be dealing with, we will lean on a decent number of high-quality unit tests.

We need to be easy to adopt for our two core use cases, and generally useful beyond those cases for anyone working with an agentic loop.

There may be more to discuss, including session management, statelessness, and modularity. The architecture/design of this library is probably the first piece to resolve.
