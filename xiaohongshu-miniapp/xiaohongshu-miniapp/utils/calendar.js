function formatDate(value) {
  const date = value instanceof Date ? value : parseDate(value);
  if (!date || Number.isNaN(date.getTime())) return '';
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${year}-${month}-${day}`;
}

function parseDate(value) {
  if (value instanceof Date) return new Date(value.getFullYear(), value.getMonth(), value.getDate());
  const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(String(value || ''));
  if (!match) return null;
  const date = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  return formatDate(date) === `${match[1]}-${match[2]}-${match[3]}` ? date : null;
}

function addDays(value, days) {
  const date = parseDate(value);
  if (!date) return null;
  date.setDate(date.getDate() + Number(days || 0));
  return date;
}

function compareDates(left, right) {
  const leftDate = parseDate(left);
  const rightDate = parseDate(right);
  if (!leftDate || !rightDate) return 0;
  return leftDate.getTime() - rightDate.getTime();
}

function isDateWithin(value, minDate, maxDate) {
  const date = parseDate(value);
  if (!date) return false;
  return (!minDate || compareDates(date, minDate) >= 0) && (!maxDate || compareDates(date, maxDate) <= 0);
}

function monthStart(value) {
  const date = parseDate(value) || new Date();
  return new Date(date.getFullYear(), date.getMonth(), 1);
}

function monthTitle(value) {
  const date = monthStart(value);
  return `${date.getFullYear()}年${date.getMonth() + 1}月`;
}

function buildCalendarCells(value, minDate, maxDate, selectedDate) {
  const first = monthStart(value);
  const offset = first.getDay() === 0 ? 6 : first.getDay() - 1;
  const days = new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate();
  const cells = [];
  for (let index = 0; index < Math.ceil((offset + days) / 7) * 7; index += 1) {
    const day = index - offset + 1;
    if (day < 1 || day > days) {
      cells.push({ key: `blank-${index}`, isDate: false, className: 'calendar-day blank' });
      continue;
    }
    const iso = formatDate(new Date(first.getFullYear(), first.getMonth(), day));
    const isDisabled = !isDateWithin(iso, minDate, maxDate);
    cells.push({
      key: iso,
      isDate: true,
      iso,
      label: String(day),
      isDisabled,
      className: isDisabled ? 'calendar-day disabled' : (iso === selectedDate ? 'calendar-day selected' : 'calendar-day')
    });
  }
  return cells;
}

function buildDateChips(now, minDate, maxDate, selectedDate) {
  const today = parseDate(now) || new Date();
  return [
    { label: '今天', iso: formatDate(today) },
    { label: '明天', iso: formatDate(addDays(today, 1)) }
  ].map(item => {
    const isDisabled = !isDateWithin(item.iso, minDate, maxDate);
    return {
      ...item,
      dateText: `${Number(item.iso.slice(5, 7))}月${Number(item.iso.slice(8, 10))}日`,
      isDisabled,
      className: isDisabled ? 'date-chip disabled' : (item.iso === selectedDate ? 'date-chip selected' : 'date-chip')
    };
  });
}

function canMoveMonth(value, direction, minDate, maxDate) {
  const month = monthStart(value);
  const target = new Date(month.getFullYear(), month.getMonth() + Number(direction || 0), 1);
  const targetEnd = new Date(target.getFullYear(), target.getMonth() + 1, 0);
  return (!minDate || compareDates(targetEnd, minDate) >= 0) && (!maxDate || compareDates(target, maxDate) <= 0);
}

module.exports = {
  addDays,
  buildCalendarCells,
  buildDateChips,
  canMoveMonth,
  compareDates,
  formatDate,
  isDateWithin,
  monthStart,
  monthTitle,
  parseDate
};
