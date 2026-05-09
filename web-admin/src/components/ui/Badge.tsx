import type { PropsWithChildren } from 'react';
import { cn } from '../../lib/utils';

export function Badge({ children, className }: PropsWithChildren<{ className?: string }>) {
  return <span className={cn('inline-flex items-center rounded-full bg-zinc-100 px-2 py-0.5 text-xs font-medium text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300 dark:border dark:border-zinc-700', className)}>{children}</span>;
}
