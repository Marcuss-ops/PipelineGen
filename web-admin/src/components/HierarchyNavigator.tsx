import { ChevronRight, Folder, Home } from 'lucide-react';
import { AssetNode } from '../api/assets';
import { Button } from './ui/Button';

interface HierarchyNavigatorProps {
  breadcrumb: AssetNode[];
  onNavigate: (id: string | 'root') => void;
}

export function HierarchyNavigator({ breadcrumb, onNavigate }: HierarchyNavigatorProps) {
  return (
    <nav className="flex items-center space-x-1 text-sm text-zinc-500 mb-4 bg-zinc-50 p-3 rounded-2xl border border-zinc-200 dark:bg-zinc-900/50 dark:border-zinc-800 dark:text-zinc-400">
      <Button 
        variant="ghost" 
        size="sm" 
        className="h-8 px-2"
        onClick={() => onNavigate('root')}
      >
        <Home className="h-4 w-4 mr-1" />
        Root
      </Button>
      
      {breadcrumb.map((node, index) => (
        <div key={node.id} className="flex items-center">
          <ChevronRight className="h-4 w-4 mx-1 text-zinc-300" />
            <Button 
              variant="ghost" 
              size="sm" 
              className={`h-8 px-2 ${index === breadcrumb.length - 1 ? 'text-zinc-900 font-bold dark:text-zinc-100' : ''}`}
              onClick={() => onNavigate(node.id)}
            >
            {node.is_folder && <Folder className="h-4 w-4 mr-1 text-blue-500" />}
            {node.name}
          </Button>
        </div>
      ))}
    </nav>
  );
}
