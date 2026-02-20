---
title: "Skills"
---

Skills are reusable knowledge packs that you attach to agents. Think of them as "expertise modules" — a skill contains instructions and reference files that teach an agent how to handle a specific domain or task.

Without skills, all of an agent's knowledge lives in its system prompt. That works fine for simple agents, but as the prompt grows with product catalogs, coding standards, response templates, and domain-specific rules, it becomes impossible to manage. Skills let you break that knowledge into independent, reusable pieces.

## How skills work

A skill has three parts:

| Field | Description |
|-------|-------------|
| `name` | Display name — appears in agent skill toggles |
| `description` | What this skill is for (your reference) |
| `instructions` | The knowledge and rules the agent should follow when using this skill |

At runtime, when an agent with skills receives a message, Magec injects the skill instructions into the agent's context — right alongside the system prompt. The agent sees them as part of its knowledge and uses them naturally.

### Reference files

Skills can also carry **reference files** — documents, templates, schemas, catalogs, or any text file that the agent needs as context. These files are uploaded through the Admin UI and stored on the server. When the agent runs, Magec reads the file contents and includes them in the agent's context alongside the skill instructions.

This is perfect for:
- Product catalogs or price lists
- API schemas or documentation
- Response templates
- Compliance rules or style guides
- Any structured data the agent needs to reference

Reference files are stored at `data/skills/{skillId}/` on the server. The store only tracks metadata (filename and size) — the actual content lives on disk, keeping the store lightweight.

## Creating a skill

In the Admin UI, go to **Skills** and click **New**.

Write the **instructions** as if you were briefing a new team member on this specific area. Be direct and specific — the agent will follow these instructions literally.

**Example instructions:**

- *"You are an expert on our return policy. Customers can return items within 30 days of purchase with a receipt. Electronics have a 15-day window. Opened software cannot be returned. Always be empathetic and offer alternatives when a return isn't possible."*
- *"When writing TypeScript code, follow our style guide: use functional components, prefer `const` over `let`, always add return types to functions, use Zod for validation. See the attached style-guide.md for the full rules."*

To add reference files, use the **drag & drop zone** in the References section. You can add files before saving — they'll be uploaded when you hit Save.

Each file in the list has a **download button** (to recover the original) and a **delete button** (to remove it from the skill).

## Connecting skills to agents

After creating a skill, enable it on specific agents:

1. Open an agent in the Admin UI
2. Expand the **Skills** section
3. Toggle on the skills you want this agent to use
4. Save

Each agent can have different skills enabled. A "customer support" agent might have skills for returns, shipping, and product knowledge. A "developer assistant" might have skills for your coding standards and API documentation. You compose each agent's expertise by selecting which skills it gets.

## Example skills

| Skill | Instructions (summary) | References |
|-------|----------------------|------------|
| Return Policy | Rules for 30-day returns, electronics exceptions, empathy guidelines | `return-policy.pdf` |
| Product Catalog | How to recommend products, upselling rules | `catalog-2025.csv`, `pricing.json` |
| TypeScript Standards | Coding conventions, linting rules, preferred patterns | `style-guide.md`, `tsconfig.json` |
| GDPR Compliance | Data handling rules, user rights, required disclaimers | `gdpr-checklist.md` |
| Meeting Notes | Template for structuring meeting summaries | `template.md` |

## Skills vs. system prompt

You might wonder: why not just put everything in the system prompt? You can — and for simple agents, that's perfectly fine. Skills become valuable when:

- **You reuse the same knowledge across multiple agents.** A product catalog skill can be shared by your support agent, your sales agent, and your FAQ bot. Update the catalog once, all agents see the change.
- **Your agent's knowledge changes frequently.** Updating a skill (or replacing a reference file) is easier than editing a massive system prompt.
- **You want to compose agents from building blocks.** Toggle skills on and off to quickly change what an agent knows, without touching the core system prompt.
- **Your prompts are getting too long to manage.** Breaking a 2,000-word prompt into a focused system prompt + 3-4 skills is much easier to maintain.

The system prompt defines *who* the agent is. Skills define *what* it knows.

{{< callout type="info" >}}
Skills are configured globally and then enabled per-agent — the same pattern as MCP Servers. Create a skill once, share it across as many agents as you want.
{{< /callout >}}

## Hot-reload

Changes to skills take effect on the next message. Edit instructions, upload new reference files, or toggle skills on different agents — save, and it's live. No restart needed.
