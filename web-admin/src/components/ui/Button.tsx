import type { ButtonHTMLAttributes, PropsWithChildren } from 'react';
import { cn } from '../../lib/utils';

type ButtonProps = PropsWithChildren<ButtonHTMLAttributes<HTMLButtonElement>> & {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
  size?: 'sm' | 'md';
};

const variants = {
  primary: 'bg-zinc-950 text-white hover:bg-black dark:bg-white dark:text-zinc-950 dark:hover:bg-zinc-200',
  secondary: 'bg-white border border-zinc-200 text-zinc-900 hover:bg-zinc-50 dark:bg-zinc-900 dark:border-zinc-800 dark:text-zinc-50 dark:hover:bg-zinc-800',
  ghost: 'text-zinc-600 hover:text-zinc-950 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:text-zinc-50 dark:hover:bg-zinc-800',
  danger: 'bg-red-600 text-white hover:bg-red-700',
};

const sizes = {
  sm: 'h-8 px-3 text-xs',
  md: 'h-9 px-3.5 text-sm',
};

export function Button({ className, variant = 'primary', size = 'md', ...props }: ButtonProps) {
  return <button className={cn('inline-flex items-center justify-center gap-2 rounded-lg font-medium transition disabled:opacity-50 disabled:pointer-events-none', variants[variant], sizes[size], className)} {...props} />;
}
