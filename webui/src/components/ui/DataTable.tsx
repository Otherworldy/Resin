import { type ColumnDef, flexRender, getCoreRowModel, useReactTable } from "@tanstack/react-table";

type DataTableProps<T> = {
    data: T[];
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    columns: ColumnDef<T, any>[];
    onRowClick?: (row: T) => void;
    selectedRowId?: string;
    getRowId?: (row: T) => string;
    className?: string;
    wrapClassName?: string;
    // Multi-select support. When selectable, a checkbox column is injected.
    selectable?: boolean;
    selectedRowIds?: ReadonlySet<string>;
    onToggleRowSelect?: (id: string) => void;
    onToggleAllRows?: () => void;
};

export function DataTable<T>({
    data,
    columns,
    onRowClick,
    selectedRowId,
    getRowId,
    className,
    wrapClassName,
    selectable = false,
    selectedRowIds,
    onToggleRowSelect,
    onToggleAllRows,
}: DataTableProps<T>) {
    // TanStack Table returns mutable table helpers; React Compiler intentionally skips memoizing here.
    // eslint-disable-next-line react-hooks/incompatible-library
    const table = useReactTable({
        data,
        columns,
        getCoreRowModel: getCoreRowModel(),
        getRowId,
    });

    const allSelected = selectable && table.getRowModel().rows.length > 0 && table.getRowModel().rows.every((row) => selectedRowIds?.has(row.id));

    return (
        <div className={`data-table-wrap ${wrapClassName ?? ""}`}>
            <table className={`data-table ${className ?? ""}`}>
                <thead>
                    {table.getHeaderGroups().map((headerGroup) => (
                        <tr key={headerGroup.id}>
                            {selectable && onToggleRowSelect && onToggleAllRows ? (
                                <th style={{ width: 32 }}>
                                    <input
                                        type="checkbox"
                                        aria-label="select all"
                                        checked={allSelected}
                                        onChange={onToggleAllRows}
                                    />
                                </th>
                            ) : null}
                            {headerGroup.headers.map((header) => (
                                <th key={header.id}>
                                    {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                                </th>
                            ))}
                        </tr>
                    ))}
                </thead>
                <tbody>
                    {table.getRowModel().rows.map((row) => {
                        const isSelected = selectedRowId != null && row.id === selectedRowId;
                        return (
                            <tr
                                key={row.id}
                                className={isSelected ? "data-table-row-selected" : onRowClick ? "clickable-row" : undefined}
                                onClick={onRowClick ? () => onRowClick(row.original) : undefined}
                            >
                                {selectable && onToggleRowSelect ? (
                                    <td style={{ width: 32 }} onClick={(event) => event.stopPropagation()}>
                                        <input
                                            type="checkbox"
                                            aria-label={`select ${row.id}`}
                                            checked={selectedRowIds?.has(row.id) ?? false}
                                            onChange={() => onToggleRowSelect(row.id)}
                                        />
                                    </td>
                                ) : null}
                                {row.getVisibleCells().map((cell) => (
                                    <td key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</td>
                                ))}
                            </tr>
                        );
                    })}
                </tbody>
            </table>
        </div>
    );
}
