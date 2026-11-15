import { ButtonHTMLAttributes, forwardRef } from 'react';
import { cn } from '../../lib/utils';

export const Button = forwardRef<HTMLButtonElement, ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'default' | 'outline' | 'ghost'; size?: 'sm' | 'default' }>(function Button({ className, variant='outline', size='default', ...props }, ref) {
  return <button ref={ref} className={cn('ui-button', `ui-button-${variant}`, size === 'sm' && 'ui-button-sm', className)} {...props} />;
});
