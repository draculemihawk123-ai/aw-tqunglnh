import { describe, expect, it } from 'vitest';
import { parseSSEBuffer } from './sse';

describe('parseSSEBuffer', () => {
  it('parses a single complete record', () => {
    const { events, remainder } = parseSSEBuffer('id: 42\nevent: project.invalidated\ndata: {"journalPosition":42}\n\n');
    expect(events).toEqual([{ id: '42', event: 'project.invalidated', data: '{"journalPosition":42}' }]);
    expect(remainder).toBe('');
  });

  it('parses multiple records in one chunk', () => {
    const { events, remainder } = parseSSEBuffer(
      'id: 1\nevent: project.invalidated\ndata: {"journalPosition":1}\n\n' +
      'id: 2\nevent: project.invalidated\ndata: {"journalPosition":2}\n\n',
    );
    expect(events).toHaveLength(2);
    expect(events[0].id).toBe('1');
    expect(events[1].id).toBe('2');
    expect(remainder).toBe('');
  });

  it('leaves an incomplete trailing record as the remainder for the next chunk', () => {
    const { events, remainder } = parseSSEBuffer('id: 1\nevent: project.invalidated\ndata: {"journalPosition":1}\n\nid: 2\nevent: proj');
    expect(events).toHaveLength(1);
    expect(remainder).toBe('id: 2\nevent: proj');
  });

  it('a comment-only heartbeat record produces no id/event/data', () => {
    const { events } = parseSSEBuffer(': heartbeat\n\n');
    expect(events).toEqual([{}]);
  });

  it('joins a multi-line data field with newlines, per the SSE spec', () => {
    const { events } = parseSSEBuffer('event: stream.disconnected\ndata: line one\ndata: line two\n\n');
    expect(events[0].data).toBe('line one\nline two');
  });

  it('reassembles a record split across two chunks', () => {
    const first = parseSSEBuffer('id: 1\nevent: project.invalid');
    expect(first.events).toHaveLength(0);
    const second = parseSSEBuffer(first.remainder + 'ated\ndata: {"journalPosition":1}\n\n');
    expect(second.events).toEqual([{ id: '1', event: 'project.invalidated', data: '{"journalPosition":1}' }]);
  });
});
