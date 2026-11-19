const script = document.querySelector('script[data-docs]');
const siteRoot = new URL('../', script.src);
const searchDialog = document.querySelector('#search-dialog');
const searchInput = document.querySelector('#search-input');
const results = document.querySelector('#search-results');
const status = document.querySelector('#search-status');
let indexPromise;
let searchNumber = 0;

function searchIndex() {
  if (indexPromise) return indexPromise;
  // A classic script can load local files. Fetch and module scripts require an HTTP origin.
  indexPromise = new Promise((resolve, reject) => {
    const source = document.createElement('script');
    source.src = new URL('assets/search-index.js', siteRoot);
    source.onload = () => resolve(globalThis.gentSearchIndex);
    source.onerror = () => {
      indexPromise = undefined;
      source.remove();
      reject(new Error('Could not load the search index.'));
    };
    document.head.append(source);
  });
  return indexPromise;
}

if (!navigator.platform.includes('Mac')) {
  document.querySelector('#search-open kbd').textContent = 'Ctrl K';
}
const sidebar = document.querySelector('.sidebar');
const selected = sidebar.querySelector('[aria-current="page"]');
if (selected && selected.offsetTop > sidebar.clientHeight - 60) {
  sidebar.scrollTop = selected.offsetTop - sidebar.clientHeight / 2;
}

function openSearch() {
  if (!searchDialog.open) searchDialog.showModal();
  searchInput.focus();
}

document.querySelector('#search-open').addEventListener('click', openSearch);
document.addEventListener('keydown', (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
    event.preventDefault();
    openSearch();
  }
});

searchInput.addEventListener('input', async () => {
  const number = ++searchNumber;
  const query = searchInput.value.trim().toLowerCase();
  results.replaceChildren();
  if (!query) {
    status.textContent = 'Search pages and public API names.';
    return;
  }
  status.textContent = 'Searching…';
  try {
    const index = await searchIndex();
    if (number !== searchNumber) return;
    const terms = query.split(/\s+/);
    const matches = index.map((entry) => {
      const title = entry.title.toLowerCase();
      const text = `${title} ${entry.text}`.toLowerCase();
      if (!terms.every((term) => text.includes(term))) return null;
      const score = (title === query ? 100 : 0) + (title.includes(query) ? 20 : 0)
        + (entry.group.startsWith('API') ? 0 : 5);
      return { entry, score };
    }).filter(Boolean).sort((a, b) => b.score - a.score).slice(0, 16);
    status.textContent = matches.length
      ? `${matches.length} results` : 'No results. Try another term.';
    for (const { entry } of matches) {
      const link = document.createElement('a');
      link.href = new URL(entry.url, siteRoot);
      const group = document.createElement('small');
      group.textContent = entry.group;
      const title = document.createElement('strong');
      title.textContent = entry.title;
      const detail = document.createElement('p');
      detail.textContent = entry.text.slice(0, 150);
      link.append(group, title, detail);
      results.append(link);
    }
  } catch (error) {
    if (number === searchNumber) {
      status.textContent = `Search is unavailable. Use the sidebar. ${error.message}`;
    }
  }
});

searchDialog.addEventListener('keydown', (event) => {
  const links = [...results.querySelectorAll('a')];
  if (!links.length) return;
  const current = links.indexOf(document.activeElement);
  if (event.key === 'ArrowDown') {
    event.preventDefault();
    links[(current + 1) % links.length].focus();
  } else if (event.key === 'ArrowUp') {
    event.preventDefault();
    if (current <= 0) searchInput.focus();
    else links[current - 1].focus();
  } else if (event.key === 'Enter' && document.activeElement === searchInput) {
    event.preventDefault();
    links[0].click();
  }
});

const menu = document.querySelector('#menu-dialog');
document.querySelector('#menu-open').addEventListener('click', () => menu.showModal());
for (const dialog of [searchDialog, menu]) {
  dialog.addEventListener('click', (event) => {
    if (event.target !== dialog) return;
    const bounds = dialog.getBoundingClientRect();
    if (event.clientX < bounds.left || event.clientX > bounds.right
      || event.clientY < bounds.top || event.clientY > bounds.bottom) dialog.close();
  });
}

for (const button of document.querySelectorAll('.copy')) {
  button.addEventListener('click', async () => {
    const code = button.closest('figure').querySelector('code').textContent;
    try {
      await navigator.clipboard.writeText(code.replace(/\n$/, ''));
      button.textContent = 'Copied';
      document.querySelector('#copy-status').textContent = 'Code copied.';
      setTimeout(() => { button.textContent = 'Copy'; }, 1800);
    } catch {
      document.querySelector('#copy-status').textContent = 'Select the code and copy it manually.';
      button.textContent = 'Select code';
      const selection = window.getSelection();
      const range = document.createRange();
      range.selectNodeContents(button.closest('figure').querySelector('code'));
      selection.removeAllRanges();
      selection.addRange(range);
    }
  });
}

const sections = document.querySelectorAll('main h2[id]');
if ('IntersectionObserver' in window) {
  const observer = new IntersectionObserver((entries) => {
    const active = entries.find((entry) => entry.isIntersecting);
    if (!active) return;
    for (const link of document.querySelectorAll('.page-toc a')) {
      if (link.hash === `#${active.target.id}`) link.setAttribute('aria-current', 'location');
      else link.removeAttribute('aria-current');
    }
  }, { rootMargin: '-90px 0px -65% 0px' });
  sections.forEach((section) => observer.observe(section));
}
