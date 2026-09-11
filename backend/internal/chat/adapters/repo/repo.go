// Package repo is the driven adapter that backs the chat ports with
// Postgres. Every repository routes its query through postgres.Conn(ctx, pool)
// so it transparently participates in any TxManager.WithinTx scope.
package repo

import "github.com/jackc/pgx/v5/pgxpool"

// Repositories bundles every concrete repository the chat module needs.
// Add new fields here when introducing a new aggregate.
type Repositories struct {
	Providers     *ProviderRepo
	Agents        *AgentRepo
	Conversations *ConversationRepo
	Messages      *MessageRepo
	Memories      *MemoryRepo
	Sources       *SourceRepo
	// AgentTools is the authorization store; ToolCalls is the audit trail.
	// Two repositories because they answer two different questions: what an
	// agent may do, and what it actually did.
	AgentTools *AgentToolRepo
	ToolCalls  *ToolCallRepo
}

func New(pool *pgxpool.Pool) *Repositories {
	return &Repositories{
		Providers:     NewProviderRepo(pool),
		Agents:        NewAgentRepo(pool),
		Conversations: NewConversationRepo(pool),
		Messages:      NewMessageRepo(pool),
		Memories:      NewMemoryRepo(pool),
		Sources:       NewSourceRepo(pool),
		AgentTools:    NewAgentToolRepo(pool),
		ToolCalls:     NewToolCallRepo(pool),
	}
}
