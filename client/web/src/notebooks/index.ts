import { Remote } from 'comlink'
import { Observable } from 'rxjs'

import { FlatExtensionHostAPI } from '@sourcegraph/shared/src/api/contract'
import { FetchFileParameters, HighlightRange } from '@sourcegraph/shared/src/components/CodeExcerpt'
import { IHighlightLineRange, SymbolKind } from '@sourcegraph/shared/src/schema'
import { AggregateStreamingSearchResults } from '@sourcegraph/shared/src/search/stream'
import { UIRangeSpec } from '@sourcegraph/shared/src/util/url'

export type BlockType = 'md' | 'query' | 'file' | 'compute' | 'symbol'

interface BaseBlock<I, O> {
    id: string
    type: BlockType
    input: I
    output: O | null
}

export interface QueryBlock extends BaseBlock<string, Observable<AggregateStreamingSearchResults>> {
    type: 'query'
}

export interface MarkdownBlock extends BaseBlock<string, string> {
    type: 'md'
}

export interface FileBlockInput {
    repositoryName: string
    revision: string
    filePath: string
    lineRange: IHighlightLineRange | null
}

export interface FileBlock extends BaseBlock<FileBlockInput, Observable<string[] | Error>> {
    type: 'file'
}

export interface ComputeBlock extends BaseBlock<string, string> {
    type: 'compute'
}

export interface SymbolBlockInput {
    repositoryName: string
    revision: string
    filePath: string
    symbolName: string
    symbolKind: SymbolKind
    symbolContainerName: string
    lineContext: number
}

export interface SymbolBlockOutput {
    symbolFoundAtLatestRevision: boolean
    effectiveRevision: string
    symbolRange: UIRangeSpec['range']
    highlightLineRange: IHighlightLineRange
    highlightedLines: string[]
    highlightSymbolRange: HighlightRange
}

export interface SymbolBlock extends BaseBlock<SymbolBlockInput, Observable<SymbolBlockOutput | Error>> {
    type: 'symbol'
}

export type Block = QueryBlock | MarkdownBlock | FileBlock | ComputeBlock | SymbolBlock

export type BlockInput =
    | Pick<FileBlock, 'type' | 'input'>
    | Pick<MarkdownBlock, 'type' | 'input'>
    | Pick<QueryBlock, 'type' | 'input'>
    | Pick<ComputeBlock, 'type' | 'input'>
    | Pick<SymbolBlock, 'type' | 'input'>

export type BlockInit =
    | Omit<FileBlock, 'output'>
    | Omit<MarkdownBlock, 'output'>
    | Omit<QueryBlock, 'output'>
    | Omit<ComputeBlock, 'output'>
    | Omit<SymbolBlock, 'output'>

export type SerializableBlock =
    | Pick<FileBlock, 'type' | 'input'>
    | Pick<MarkdownBlock, 'type' | 'input'>
    | Pick<QueryBlock, 'type' | 'input'>
    | Pick<ComputeBlock, 'type' | 'input'>
    | Pick<SymbolBlock, 'type' | 'input' | 'output'>

export type BlockDirection = 'up' | 'down'

export interface BlockProps<T extends Block = Block> {
    isReadOnly: boolean
    isSelected: boolean
    isOtherBlockSelected: boolean
    id: T['id']
    input: T['input']
    output: T['output']
    onRunBlock(id: string): void
    onDeleteBlock(id: string): void
    onBlockInputChange(id: string, blockInput: BlockInput): void
    onMoveBlock(id: string, direction: BlockDirection): void
    onDuplicateBlock(id: string): void
}

export interface BlockDependencies {
    extensionHostAPI: Promise<Remote<FlatExtensionHostAPI>>
    fetchHighlightedFileLineRanges: (parameters: FetchFileParameters, force?: boolean) => Observable<string[][]>
}
