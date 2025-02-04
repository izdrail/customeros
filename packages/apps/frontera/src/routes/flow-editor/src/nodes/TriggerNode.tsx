import { MouseEventHandler } from 'react';

import { observer } from 'mobx-react-lite';
import { NodeProps, ViewportPortal } from '@xyflow/react';

import { IconButton } from '@ui/form/IconButton';
import { useStore } from '@shared/hooks/useStore';
import { UserPlus01 } from '@ui/media/icons/UserPlus01';
import { ChevronDown } from '@ui/media/icons/ChevronDown';
import { Lightning01 } from '@ui/media/icons/Lightning01';

import { Handle } from '../components';
import { DropdownCommandMenu } from '../commands/Commands';

export const TriggerNode = (
  props: NodeProps & { data: Record<string, string> },
) => {
  const { ui } = useStore();

  const handleOpen: MouseEventHandler<HTMLButtonElement> = (e) => {
    e.stopPropagation();
    ui.flowCommandMenu.setOpen(true);
    ui.flowCommandMenu.setType('TriggersHub');
    ui.flowCommandMenu.setContext({
      id: props.id,
      entity: 'Trigger',
    });
  };

  return (
    <>
      <div
        className={`h-[83px] w-[300px] bg-white border border-grayModern-300 rounded-lg group relative cursor-pointer flex flex-col items-center`}
      >
        <div className='px-4 bg-gray-25 text-xs h-full flex items-center w-full rounded-t-lg justify-center border-b border-dashed border-gray-300 text-gray-500'>
          Flow triggers when
        </div>

        <div className='flex items-center justify-between w-full p-4  h-[56px]'>
          <div className='truncate text-sm flex items-center'>
            <div className='size-6 mr-2 bg-gray-50 border border-gray-100 rounded flex items-center justify-center'>
              {props.data.entity && props.data?.triggerType ? (
                <UserPlus01 className='text-gray-500 ' />
              ) : (
                <Lightning01 className='text-gray-500' />
              )}
            </div>

            {props.data.entity && props.data.triggerType ? (
              <span className='font-medium '>
                <span className='capitalize mr-1'>
                  {props.data.entity?.toLowerCase() ?? 'Record'}
                </span>
                added manually
              </span>
            ) : (
              <span
                role={'button'}
                onClick={handleOpen}
                className='text-gray-400'
                data-test={'flow-trigger-block'}
              >
                Choose a trigger…
              </span>
            )}
          </div>

          <IconButton
            size='xxs'
            variant='ghost'
            aria-label='Edit'
            onClick={handleOpen}
            icon={<ChevronDown />}
            className='ml-2 opacity-0 group-hover:opacity-100 pointer-events-all'
          />
        </div>
        <Handle type='target' />
        <Handle type='source' />
      </div>
      <TriggerViewportPortal
        id={props.id}
        positionAbsoluteX={props.positionAbsoluteX}
        positionAbsoluteY={props.positionAbsoluteY}
      />
    </>
  );
};

export const TriggerViewportPortal = observer(
  ({
    id,
    positionAbsoluteX,
    positionAbsoluteY,
  }: {
    id: string;
    positionAbsoluteX: number;
    positionAbsoluteY: number;
  }) => {
    const { ui } = useStore();

    const showTriggerDropdown =
      ui.flowCommandMenu?.isOpen &&
      id === ui.flowCommandMenu.context.id &&
      ui.flowCommandMenu.context.entity === 'Trigger';

    return (
      <>
        {showTriggerDropdown && (
          <ViewportPortal>
            <div
              className='border border-gray-200 rounded-lg shadow-lg'
              style={{
                transform: `translate(calc(${positionAbsoluteX}px + 150px - 180px), ${
                  positionAbsoluteY + 83 + 4 // 83 is height of the node, 4 is desired spacing
                }px)`,
                position: 'absolute',
                pointerEvents: 'all',
                zIndex: 50000,
                width: '360px',
                left: '0',
                top: '0',
              }}
            >
              <DropdownCommandMenu />
            </div>
          </ViewportPortal>
        )}
      </>
    );
  },
);
