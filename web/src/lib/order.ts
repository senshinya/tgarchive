/** Where a message sits in its conversation: `pos` (a channel's posts and comments by their
 * Telegram message id, everything else by id), ties broken by id. */
export interface Ordered {
  id: number;
  pos: number;
}

/** Compares two messages by their place in the conversation. */
export function byPosition(a: Ordered, b: Ordered): number {
  return a.pos - b.pos || a.id - b.id;
}
