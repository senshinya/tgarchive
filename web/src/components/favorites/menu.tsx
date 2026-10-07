import { Star, StarOff, Tags } from 'lucide-preact';
import type { Message } from '../../api/types';
import type { Store } from '../../state/store';
import type { MenuItem } from '../../ui/ContextMenu';

/** The favorite entries of a message's menu: add or remove it, and edit its tags once it is one. */
export function favoriteMenuItems(store: Store, msg: Message, editTags: (m: Message) => void): MenuItem[] {
  if (!msg.favorite) return [{ label: '收藏', icon: <Star size={20} />, onSelect: () => void store.toggleFavorite(msg) }];
  return [
    { label: '取消收藏', icon: <StarOff size={20} />, onSelect: () => void store.toggleFavorite(msg) },
    { label: '标签…', icon: <Tags size={20} />, onSelect: () => editTags(msg) },
  ];
}
