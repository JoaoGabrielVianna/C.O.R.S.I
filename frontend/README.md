# C.O.R.S.I Frontend

SPA React que serve duas superfícies: a **landing pública** e o **workspace autenticado**.

> Este README explica **estrutura, como rodar e onde encontrar o resto**.
> A visão geral do projeto está em [`../README.md`](../README.md), e o código tem precedência
> quando houver conflito.

---

## Stack

React 19 · TypeScript 6 · Vite 8 · Tailwind v4 · TanStack Query v5 · React Router v7 ·
framer-motion · lucide-react · react-markdown

**Sem dependência de teste.** O projeto não tem harness, e nenhum arquivo `.test.*` ou
`.spec.*`. Os únicos gates automatizados são `tsc -b` e `eslint`.

---

## Como rodar

```bash
npm install
npm run dev       # vite em :5173
npm run build     # tsc -b && vite build
npm run lint      # eslint
npm run preview
```

`VITE_API_URL` define a base da API e é **embutida no bundle em build time**. Vazio significa
caminho relativo, o que funciona com o proxy do Vite em desenvolvimento e com same-origin em
produção atrás de reverse proxy.

---

## Estrutura

```text
src/
  main.tsx                 QueryClient → Theme → I18n → Auth → App
  App.tsx                  rotas

  pages/
    Landing.tsx            landing pública
    Login.tsx              formulário de login (mock, ver abaixo)
    app/
      Dashboard.tsx        tiles de texto, sem dado real
      Account.tsx
      PersonDetail.tsx     domínio de Finance
      modules/
        agents/            página do módulo Agents
        finance/           telas do módulo Finance
        job-radar/         protótipo local, backstage
        Content.tsx        placeholder
        Intelligence.tsx   placeholder
      settings/            profile · security · preferences · modules · integrations

  modules/
    agents/                api · hooks · components do módulo Agents
    finance/               api · hooks do módulo Finance
    integrations/          superfície de integrações (hoje: o protótipo do Telegram)

  lib/
    api/                   cliente HTTP único, origem do workspace
    auth/                  MOCK
    workspace/ command/ search/ notifications/ i18n/ theme/
    navVisibility.ts       única fonte com efeito sobre a navegação

  components/
    workspace/             shell: layout, sidebar, header, cards
    ui/ layout/ auth/      primitivas
    sections/ mocks/       existem apenas para a landing
```

**Duas casas por módulo, com semânticas diferentes:** `src/modules/<mod>/` é a camada de
acesso ao backend e estado de servidor; `src/pages/app/modules/<mod>/` é a camada de UI e
estado local.

---

## Estado dos módulos

| Módulo | Estado |
|---|---|
| **Agents** | **Ativo.** Único módulo visível na navegação, e destino default do login |
| **Finance** | **Implementado, mas oculto da navegação.** A rota continua montada e alcançável por URL direta. Parte dos dados ainda vive no navegador |
| **Job Radar** | **Backstage / protótipo.** 100% `localStorage`, sem backend. Não evolui e não é removido |
| Content, Intelligence | Placeholders. Uma tela vazia cada |

A visibilidade é controlada por `lib/navVisibility.ts`, que esconde cinco rotas e define
`DEFAULT_APP_ROUTE`. **É a única fonte com efeito**: as rotas continuam montadas em
`App.tsx`.

---

## API client

`lib/api/client.ts` é o **único wrapper de fetch** da aplicação: injeta `X-Workspace-Id`,
serializa JSON, aplica timeout de 15s e converte não-2xx em `ApiError` tipado, que espelha o
formato de erro do backend.

**Duas exceções conhecidas:**

| Exceção | Motivo |
|---|---|
| `modules/agents/api/stream.ts` | Precisa consumir o corpo incrementalmente para SSE. Reusa o `API_BASE` exportado. **`EventSource` não serve**: não envia header nem body, e todo request é workspace-scoped |
| `modules/agents/api/fx.ts` | Chama um host externo de câmbio direto. Única chamada de rede do browser fora do wrapper |

---

## Autenticação e workspace

**A autenticação é um mock.** `lib/auth/AuthProvider.tsx` ignora a senha e guarda um usuário
constante em `localStorage`. `RequireAuth` protege rotas contra um estado que o próprio
navegador escreve.

**O workspace é uma constante pública.** `lib/api/workspace.ts` devolve sempre o UUID
sentinela.

Os dois são estado conhecido e registrado, não descuido. Ver
o estado atual da camada de Platform.

---

## i18n e tema

`lib/i18n/` tem **PT-BR como fonte canônica** (`pt.ts`) e EN espelhado por
`Translations = typeof pt`, o que faz o **TypeScript exigir paridade de chaves**. Novas
chaves entram primeiro em `pt.ts`.

`lib/theme/` cobre light, dark e system, com script inline no `index.html` contra FOUC.

---

## Landing

`pages/Landing.tsx` compõe Navbar, Hero, Manifesto, Modules, PlatformReveal, LabNotes e
Footer. `components/sections/` e `components/mocks/` existem apenas para ela.

A landing separa **estado atual** de **próximas camadas**. Capacidade que não existe fica na
seção de futuro, nunca na de estado atual.

---

## Documentação profunda

| Assunto | Onde |
|---|---|
| Visão geral do projeto | [`../README.md`](../README.md) |
| Backend e contratos | [`../backend/README.md`](../backend/README.md) |
