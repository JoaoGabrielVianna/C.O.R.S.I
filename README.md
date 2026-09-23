# C.O.R.S.I.

Sistema operacional pessoal. Operador único. Go + React. Roda localmente, sem ambiente
de produção ativo.

> A ferramenta que eu uso todo dia, não uma demo. Os limites documentados aqui são
> limites reais, encontrados usando o sistema.

---

## O que é

Uma plataforma onde eu converso com agentes que têm capabilities explicitamente
autorizadas e auditadas, registro dinheiro num ledger com contrato de totais congelado,
acompanho oportunidades de trabalho e rascunho conteúdo.

Não é um assistente genérico com acesso ao meu computador. Cada coisa que um agente
consegue fazer é uma capability declarada, autorizada por agente, registrada no audit
no instante em que executa.

## Módulos

| Módulo | O que faz | Versão |
|---|---|---|
| **Agents** | agentes com capabilities auditadas e receipts de execução | `1.6.0` |
| **Finance** | ledger em centavos, com contrato de totais congelado | `1.1.0` |
| **Threads** | ideias e rascunhos, operados pelo chat | `1.0.0` |
| **Job Radar** | pipeline de oportunidades | `1.0.0` |
| **GitHub** | integração de leitura | ativa |
| **Meta Threads** | integração de leitura | congelada por decisão |

## Duas ideias que sustentam o resto

**Nenhuma afirmação de escrita sem receipt de escrita.**
Um agente financeiro respondeu *"8 transações importadas"* num turno com **zero tool
calls**, contra um ledger vazio. O runtime estava certo e a pessoa foi informada do
contrário. Hoje todo turno assistente carrega um `WriteReceipt` derivado da execução,
nunca da prosa, com `REQUESTED` / `EXECUTED` / `FAILED` / `NOT_EXECUTED`. A garantia é
precisa: **isso não impede o modelo de escrever uma frase falsa; impede o produto de
apresentá-la como confirmação objetiva de escrita.**

**Nenhuma afirmação externa verificada sem leitura externa atual.**
O mesmo defeito na direção da leitura: um agente de conteúdo afirmou *"1.535 seguidores,
40.055 views"* num turno sem nenhuma tool call. Os números reais eram **163 e 224**.
Hoje uma capability declara sobre si mesma se fala com um sistema externo, e todo turno
carrega um `ReadReceipt` com `VERIFIED_EXTERNAL_READ` / `FAILED_EXTERNAL_READ` /
`NO_EXTERNAL_READ`. Para leitura, o estado perigoso é a **ausência** — então é o
silêncio que recebe rótulo.

Nos dois casos, **nada lê a resposta do modelo**. Não existe detecção de claim, nenhuma
heurística procurando números na prosa. O receipt vem da execução ou não vem.

## Arquitetura

```text
MODULES        Agents · Finance · Threads · Job Radar
INTEGRATIONS   camada de adaptação com sistemas e formatos externos
PLATFORM       capacidades técnicas compartilhadas
```

Regra de dependência:

```text
Module → Integration → External
Module → Platform
Integration → Platform
```

Cada bounded context tem **schema e timeline de migration próprios**, com tabela de
versão própria. Hoje são nove: `schema_migrations` (finance), `schema_migrations_chat`,
`_releases`, `_github`, `_jobradar`, `_threads`, `_metathreads`, `_palace` e
`_telegram`. O entrypoint aplica todas no start e aborta se qualquer uma falhar.
Forward-only em banco populado.

Dinheiro é **`int64` de centavos** em toda camada, sem float e sem string parseável em
lugar nenhum. Valor não tem sinal: a direção vem da categoria.

## Stack

**Backend** Go 1.25 · chi v5 · pgx v5 · golang-migrate · Prometheus · OTel v1
**Frontend** React 19 · TypeScript 6 · Vite 8 · Tailwind v4 · TanStack Query v5 · React Router v7
**Infra** PostgreSQL 17 · Docker Compose (dev)

## Setup

```bash
make doctor        # verifica docker, go, node
make dev           # postgres, migrations e backend
make frontend      # vite em :5173
make test          # unit e domínio, sem banco

make -C backend ci        # GATE oficial: inclui integração, exige docker
make -C backend ci-fast   # sem integração. NÃO é suficiente sozinho
```

Configuração em [`backend/.env.example`](backend/.env.example) e
[`frontend/.env.example`](frontend/.env.example). `make build` constrói as duas
imagens Docker, `corsi-backend` e `corsi-frontend`.

## Estado atual

**Não há ambiente de produção.** O sistema roda localmente, e é assim que ele é usado
todo dia. Versões anteriores deste README descreviam um deployment em `corsi.dev` e
`api.corsi.dev`: esse ambiente não está ativo, e a afirmação ficou obsoleta sem que o
texto acompanhasse.

**Congelado por decisão, não quebrado:** a integração com a Meta Threads tem a fundação
implementada — credencial OAuth cifrada por workspace, seis tools todas read-only — e a
ativação está adiada até o deployment em VPS, para não fixar o callback OAuth num
ambiente local transitório.

**Dívida conhecida e registrada:** releases históricas gravadas com uma shape JSON antiga
renderizam alguns campos em branco; o conserto é no leitor, porque snapshot publicado é
imutável por trigger. Sete consumidores do frontend aplicam semântica de totais própria,
em vez da do contrato.

## Roadmap público

Não há datas. O que está decidido:

- consertar a decodificação de snapshots de release legados;
- descongelar a Meta Threads quando houver deployment em VPS estável;
- convergir os consumidores de totais do frontend para o contrato.

Integrações **implementadas** são exatamente duas: GitHub (leitura, ativa) e Meta
Threads (leitura, congelada). Qualquer outra que apareça em conversa é intenção de
desenho, não capability existente.

## O que este repositório não é

- **Não tem autenticação real.** Operador único. Identity é uma capacidade **ausente** da
  plataforma, não uma feature adiada: o middleware de workspace aceita qualquer UUID
  sintaticamente válido, e o frontend envia uma constante pública.
- **Não tem CI remoto.** O gate é `make -C backend ci`, rodado à mão, e depende de
  disciplina humana.
- **Não tem harness de teste de browser.** O frontend tem testes de componente; a
  verificação visual é manual.
- **Não aceita contribuição hoje.**

## Licença

**Todos os direitos reservados.** Este repositório é público para leitura, e isso **não**
é uma licença open source: não há permissão de uso, cópia, modificação ou distribuição.

© João Corsi
