package contextassembler

// MessageCandidate is one message of a WorkItem's task chat under
// consideration for a prompt (V9-07, gap G7): what ResolveMessages needs to
// decide, and nothing of its content.
type MessageCandidate struct {
	ID string
	// Pinned messages are never omitted.
	Pinned bool
	// Bytes is the size of the message content, the same unit (UTF-8 bytes,
	// CostModelUTF8BytesV1) as the route's resource budget.
	Bytes uint64
}

// MessageBudget is the policy's `messages` block: see policy.MessageBudget.
type MessageBudget struct {
	MaxBytes   uint64
	KeepLatest uint32
}

// MessageSelection is ResolveMessages' result. Both lists keep the order of
// the candidates they came from (message sequence), and together they hold
// every candidate exactly once.
type MessageSelection struct {
	Included []string
	Omitted  []string
}

// ResolveMessages decides which messages of a task chat reach the prompt in
// full and which are reduced to a reference. candidates must be in message
// sequence order, oldest first.
//
//  1. The newest budget.KeepLatest messages and every pinned message are
//     required: they are included whatever they cost.
//  2. The remaining capacity is MaxBytes minus the required messages' size, or
//     zero when they alone exceed it. The other messages are visited newest
//     first, and one is included when it fits in what is left (a smaller older
//     message may still fit after a larger newer one did not, as in Resolve).
//  3. Every other message is omitted.
//
// So the content a prompt carries from messages is at most
// max(MaxBytes, size of the required messages), however many rounds of rework
// have appended to the chat, and the newest message is always present. The
// function is pure: the same candidates and budget always give the same
// selection.
func ResolveMessages(candidates []MessageCandidate, budget MessageBudget) MessageSelection {
	count := len(candidates)
	newestStart := 0
	if uint64(budget.KeepLatest) < uint64(count) {
		newestStart = count - int(budget.KeepLatest)
	}

	include := make([]bool, count)
	var spent uint64
	for i, c := range candidates {
		if c.Pinned || i >= newestStart {
			include[i] = true
			spent += c.Bytes
		}
	}
	var remaining uint64
	if budget.MaxBytes > spent {
		remaining = budget.MaxBytes - spent
	}
	for i := count - 1; i >= 0; i-- {
		if include[i] {
			continue
		}
		if candidates[i].Bytes <= remaining {
			include[i] = true
			remaining -= candidates[i].Bytes
		}
	}

	var selection MessageSelection
	for i, c := range candidates {
		if include[i] {
			selection.Included = append(selection.Included, c.ID)
		} else {
			selection.Omitted = append(selection.Omitted, c.ID)
		}
	}
	return selection
}
