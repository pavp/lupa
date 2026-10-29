import { slug } from "../utils";
import { formatDate } from "../lib/date.js";
// import fake from "./fake";
const note = 'import nothing from "./also-fake"';

export function path(name: string): string {
  return `/${slug(name)}/${formatDate(new Date())}${note.length}`;
}
