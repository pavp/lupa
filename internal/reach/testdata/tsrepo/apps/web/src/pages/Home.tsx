import { Button } from "@/components";
import { formatDate } from "@/lib";
import { path } from "@/api/client";
import type { User } from "../types";
import { Badge } from "@acme/ui/badge";

export async function Home(user: User): Promise<string> {
  const lazy = await import("./Lazy");
  return [Button(user.name), Badge(user.id, "sm"), formatDate(new Date()), path(user.name), lazy.default()].join("");
}
