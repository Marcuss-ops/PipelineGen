import { Database, Search, Settings, Moon, Sun } from 'lucide-react';
import { type PropsWithChildren, useEffect, useState } from 'react';
import { Button } from '../ui/Button';

export function AppShell({ children }: PropsWithChildren) {
  const [isDark, setIsDark] = useState(() => {
    if (typeof window !== 'undefined') {
      return localStorage.getItem('theme') === 'dark' || 
             (!localStorage.getItem('theme') && window.matchMedia('(prefers-color-scheme: dark)').matches);
    }
    return false;
  });

  useEffect(() => {
    if (isDark) {
      document.documentElement.classList.add('dark');
      localStorage.setItem('theme', 'dark');
    } else {
      document.documentElement.classList.remove('dark');
      localStorage.setItem('theme', 'light');
    }
  }, [isDark]);

  return (
    <div className="min-h-screen bg-zinc-50 text-zinc-950 dark:bg-zinc-950 dark:text-zinc-50 transition-colors duration-300">
      <header className="sticky top-0 z-40 border-b border-zinc-200 bg-white/90 backdrop-blur-xl dark:border-zinc-800 dark:bg-zinc-900/90">
        <div className="mx-auto flex h-14 max-w-[1800px] items-center gap-4 px-8 sm:px-10">
          <div className="flex items-center gap-3">
            <div className="grid h-8 w-8 place-items-center rounded-xl bg-zinc-950 text-white dark:bg-white dark:text-zinc-950">
              <Database className="h-4 w-4" />
            </div>
            <div>
              <h1 className="text-sm font-bold tracking-tight">PipelineGen Admin</h1>
              <p className="text-[11px] text-zinc-500 dark:text-zinc-400">Media DB console</p>
            </div>
          </div>
          <div className="ml-auto hidden items-center gap-2 rounded-full border border-zinc-200 bg-zinc-50 px-3 py-1.5 text-xs text-zinc-500 md:flex dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-400">
            <Search className="h-3.5 w-3.5" />
            Gestione asset, Drive, hash, metadata e pipeline
          </div>
          <Button 
            variant="ghost" 
            size="sm" 
            onClick={() => setIsDark(!isDark)}
            title={isDark ? "Passa a Light Mode" : "Passa a Dark Mode"}
          >
            {isDark ? <Sun className="h-4 w-4 text-amber-400" /> : <Moon className="h-4 w-4 text-zinc-500" />}
          </Button>
          <Button variant="ghost" size="sm" title="Settings">
            <Settings className="h-4 w-4" />
          </Button>
        </div>
      </header>
      <main className="mx-auto max-w-[1800px] px-8 py-8 sm:px-10">{children}</main>
    </div>
  );
}
