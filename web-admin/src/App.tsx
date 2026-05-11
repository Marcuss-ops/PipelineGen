import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useState } from 'react';
import { AppShell } from './components/layout/AppShell';
import { Button } from './components/ui/Button';
import { ArtlistRunPage } from './pages/ArtlistRunPage';
import { JobsPage } from './pages/JobsPage';
import { MediaAdminPage } from './pages/MediaAdminPage';
import { DiagnosticsPage } from './pages/DiagnosticsPage';
import { ActiveJobsOverlay } from './components/ActiveJobsOverlay';
import { Toaster } from 'react-hot-toast';

const queryClient = new QueryClient();

type Page = 'media' | 'artlist' | 'jobs' | 'diagnostics';

export default function App() {
  const [page, setPage] = useState<Page>('media');
  return (
    <QueryClientProvider client={queryClient}>
      <AppShell>
        <div className="mb-5 flex gap-2">
          <Button variant={page === 'media' ? 'primary' : 'secondary'} onClick={() => setPage('media')}>Media DB</Button>
          <Button variant={page === 'artlist' ? 'primary' : 'secondary'} onClick={() => setPage('artlist')}>Artlist Run</Button>
          <Button variant={page === 'jobs' ? 'primary' : 'secondary'} onClick={() => setPage('jobs')}>Jobs</Button>
          <Button variant={page === 'diagnostics' ? 'primary' : 'secondary'} onClick={() => setPage('diagnostics')}>System</Button>
        </div>
        {page === 'media' ? <MediaAdminPage /> : 
         page === 'artlist' ? <ArtlistRunPage /> : 
         page === 'jobs' ? <JobsPage /> : 
         <DiagnosticsPage />}
      </AppShell>
      <ActiveJobsOverlay />
      <Toaster position="top-right" />
    </QueryClientProvider>
  );
}
