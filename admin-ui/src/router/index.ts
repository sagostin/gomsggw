import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

export const router = createRouter({
  history: createWebHistory('/ui/'),
  routes: [
    { path: '/connect', name: 'connect', component: () => import('../views/ConnectView.vue'), meta: { public: true } },
    { path: '/', name: 'dashboard', component: () => import('../views/DashboardView.vue') },
    { path: '/carriers', name: 'carriers', component: () => import('../views/CarriersView.vue') },
    { path: '/clients', name: 'clients', component: () => import('../views/ClientsView.vue') },
    { path: '/clients/:id', name: 'client-detail', component: () => import('../views/ClientDetailView.vue'), props: true },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach((to) => {
  const auth = useAuthStore()
  if (!to.meta.public && !auth.isConnected) {
    return { name: 'connect', query: to.fullPath !== '/' ? { redirect: to.fullPath } : {} }
  }
  if (to.name === 'connect' && auth.isConnected) {
    return { name: 'dashboard' }
  }
})
