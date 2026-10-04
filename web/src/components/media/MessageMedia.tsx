import type { Message } from '../../api/types';
import { Animation } from './Animation';
import { Audio } from './Audio';
import { Contact, Dice, Location, Poll } from './Cards';
import { Document } from './Document';
import { Photo } from './Photo';
import { Sticker } from './Sticker';
import { Video } from './Video';
import { VideoNote } from './VideoNote';
import { Voice } from './Voice';

/** The non-text part of a single message, by kind. Text/other kinds render nothing here. */
export function MessageMedia({ msg, onOpen }: { msg: Message; onOpen: (msg: Message) => void }) {
  const open = () => onOpen(msg);
  switch (msg.kind) {
    case 'photo':
      return <Photo msg={msg} onOpen={open} />;
    case 'video':
      return <Video msg={msg} onOpen={open} />;
    case 'animation':
      return <Animation msg={msg} onOpen={open} />;
    case 'voice':
      return <Voice msg={msg} />;
    case 'audio':
      return <Audio msg={msg} />;
    case 'document':
      return <Document msg={msg} />;
    case 'sticker':
      return <Sticker msg={msg} />;
    case 'video_note':
      return <VideoNote msg={msg} />;
    case 'location':
    case 'venue':
      return <Location msg={msg} />;
    case 'contact':
      return <Contact msg={msg} />;
    case 'poll':
      return <Poll msg={msg} />;
    case 'dice':
      return <Dice msg={msg} />;
    default:
      return null;
  }
}
